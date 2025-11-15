package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/example/logpipeline/internal/config"
	"github.com/example/logpipeline/internal/domain"
	httpapi "github.com/example/logpipeline/internal/http"
	"github.com/example/logpipeline/internal/metrics"
	"github.com/example/logpipeline/internal/queue"
	"github.com/example/logpipeline/internal/service"
	"github.com/example/logpipeline/internal/storage"
)

func main() {
	addr := flag.String("addr", getEnv("HTTP_ADDR", ":8080"), "HTTP listen address")
	versionFlag := flag.Int("version", getIntEnv("PIPELINE_VERSION", 1), "Pipeline version (1 or 2)")
	logsDir := flag.String("logs-dir", getEnv("LOGS_DIR", "logs"), "Directory for raw logs")
	analyticsDir := flag.String("analytics-dir", getEnv("ANALYTICS_DIR", "analytics"), "Directory for analytics output")
	aggInterval := flag.Duration("aggregation-interval", getDurationEnv("AGGREGATION_INTERVAL", time.Minute), "Aggregation interval")
	configPath := flag.String("config", "", "Path to YAML config file (overrides related flags)")
	flag.Parse()

	logger := log.New(os.Stdout, "logpipeline ", log.LstdFlags|log.LUTC)
	metrics.Init()

	var fileCfg *config.Config
	var err error
	if strings.TrimSpace(*configPath) != "" {
		fileCfg, err = config.Load(*configPath)
		if err != nil {
			logger.Fatalf("failed to load config file: %v", err)
		}
	}

	srvAddr := *addr
	version := *versionFlag
	logsDirVal := *logsDir
	analyticsDirVal := *analyticsDir
	aggIntervalVal := *aggInterval
	var kafkaSettings *config.KafkaSettings

	if fileCfg != nil {
		version = fileCfg.Version
		srvAddr = fileCfg.Server.Addr
		logsDirVal = fileCfg.Storage.LogsDir
		analyticsDirVal = fileCfg.Storage.AnalyticsDir
		if aggIntervalVal, err = fileCfg.AggregationInterval(*aggInterval); err != nil {
			logger.Fatalf("invalid aggregation interval: %v", err)
		}
		kafkaSettings = &fileCfg.Kafka
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store := storage.NewFileLogStore(logsDirVal, logger)
	var (
		queueImpl domain.LogQueue
		mode      service.PipelineMode = service.ModeDirect
		closer    func() error
	)

	if version == 2 {
		mode = service.ModeQueue
		var kafkaQueue *queue.KafkaLogQueue
		if kafkaSettings != nil && len(kafkaSettings.Brokers) > 0 {
			batchTimeout, err := kafkaSettings.KafkaBatchTimeout(time.Second)
			if err != nil {
				logger.Fatalf("invalid kafka batch timeout: %v", err)
			}
			kafkaQueue, err = queue.NewKafkaLogQueue(queue.KafkaConfig{
				Brokers:        kafkaSettings.Brokers,
				Topic:          kafkaSettings.Topic,
				GroupID:        kafkaSettings.GroupID,
				BatchSize:      kafkaSettings.BatchSize,
				BatchTimeout:   batchTimeout,
				Consumers:      kafkaSettings.Consumers,
				RequireAllAcks: kafkaSettings.RequireAllAcks,
			}, logger)
		} else {
			kafkaQueue, err = createKafkaQueueFromEnv(logger)
		}
		if err != nil {
			logger.Fatalf("failed to configure Kafka: %v", err)
		}
		queueImpl = kafkaQueue
		closer = kafkaQueue.Close

		consumeCtx, consumeCancel := context.WithCancel(ctx)
		defer consumeCancel()
		if err := kafkaQueue.StartConsumers(consumeCtx, func(c context.Context, rec domain.LogRecord) {
			if err := store.SaveBatch(c, []domain.LogRecord{rec}); err != nil {
				metrics.IncIngestErrors()
				logger.Printf("consumer failed to persist log: %v", err)
				return
			}
			metrics.AddLogsIngested(1)
		}); err != nil {
			logger.Fatalf("failed to start Kafka consumers: %v", err)
		}
	} else {
		queueImpl = queue.NoopQueue{}
	}

	if closer != nil {
		defer closer()
	}

	ingestionSvc := service.NewIngestionService(store, queueImpl, mode, logger)

	aggSvc := service.NewAggregationService(logsDirVal, analyticsDirVal, aggIntervalVal, logger)
	aggSvc.Start(ctx)

	mux := http.NewServeMux()
	handler := httpapi.NewHandler(ingestionSvc, logger)
	handler.RegisterRoutes(mux)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:    srvAddr,
		Handler: mux,
	}

	go func() {
		logger.Printf("server listening on %s (version=%d)", srvAddr, version)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("server error: %v", err)
		}
	}()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("graceful shutdown failed: %v", err)
	}
}

func createKafkaQueueFromEnv(logger *log.Logger) (*queue.KafkaLogQueue, error) {
	brokersEnv := strings.TrimSpace(os.Getenv("KAFKA_BROKERS"))
	if brokersEnv == "" {
		return nil, errors.New("KAFKA_BROKERS must be set for version 2")
	}
	brokerParts := strings.Split(brokersEnv, ",")
	brokers := make([]string, 0, len(brokerParts))
	for _, b := range brokerParts {
		b = strings.TrimSpace(b)
		if b != "" {
			brokers = append(brokers, b)
		}
	}
	if len(brokers) == 0 {
		return nil, errors.New("no valid Kafka brokers provided")
	}

	cfg := queue.KafkaConfig{
		Brokers:        brokers,
		Topic:          getEnv("KAFKA_TOPIC", "logs"),
		GroupID:        getEnv("KAFKA_GROUP_ID", "logs-consumer-group"),
		BatchSize:      getIntEnv("KAFKA_BATCH_SIZE", 100),
		BatchTimeout:   getDurationEnv("KAFKA_BATCH_TIMEOUT", time.Second),
		Consumers:      getIntEnv("KAFKA_CONSUMERS", 1),
		RequireAllAcks: strings.EqualFold(os.Getenv("KAFKA_ACKS"), "all"),
	}

	return queue.NewKafkaLogQueue(cfg, logger)
}

func getEnv(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func getIntEnv(key string, fallback int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return fallback
	}
	return parsed
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return fallback
	}
	return d
}
