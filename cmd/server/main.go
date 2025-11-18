package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/example/logpipeline/internal/config"
	"github.com/example/logpipeline/internal/domain"
	httpapi "github.com/example/logpipeline/internal/http"
	"github.com/example/logpipeline/internal/metrics"
	"github.com/example/logpipeline/internal/profileutil"
	"github.com/example/logpipeline/internal/queue"
	"github.com/example/logpipeline/internal/service"
	"github.com/example/logpipeline/internal/storage"
	"github.com/example/logpipeline/util"
)

func main() {
	addr := flag.String("addr", util.GetEnv(util.HTTPAddr, util.DefaultHTTPAddr), "HTTP listen address")
	versionFlag := flag.Int("version", util.GetIntEnv(util.PipelineVersion, util.DefaultPipelineVersion), "Pipeline version (1 or 2)")
	logsDir := flag.String("logs-dir", util.GetEnv(util.LogsDir, util.DefaultLogsDir), "Directory for raw logs")
	analyticsDir := flag.String("analytics-dir", util.GetEnv(util.AnalyticsDir, util.DefaultAnalyticsDir), "Directory for analytics output")
	aggInterval := flag.Duration("aggregation-interval", util.GetDurationEnv(util.AggregationInterval, util.DefaultAggregationInterval), "Aggregation interval")
	configPath := flag.String("config", "", "Path to YAML config file (overrides related flags)")
	flag.Parse()

	logger := log.New(os.Stdout, "logpipeline ", log.LstdFlags|log.LUTC)
	metrics.Init()
	util.MaybeStartPprof(logger)

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

	run := func() error {
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
					return fmt.Errorf("invalid kafka batch timeout: %w", err)
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
				kafkaQueue, err = util.CreateKafkaQueueFromEnv(logger)
			}
			if err != nil {
				return fmt.Errorf("configure kafka: %w", err)
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
				return fmt.Errorf("start kafka consumers: %w", err)
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
		return nil
	}

	var runErr error
	if util.CaptureProfiles() {
		profileName := util.GetEnv(util.ProfileName, fmt.Sprintf("version%d", version))
		dir := util.GetEnv(util.ProfileDir, util.DefaultProfileDir)
		logger.Printf("profiling enabled; CPU/heap profiles under %s", dir)
		runErr = profileutil.WithProfiling(dir, profileName, logger, run)
	} else {
		runErr = run()
	}

	if runErr != nil {
		logger.Fatalf("server exited with error: %v", runErr)
	}
}
