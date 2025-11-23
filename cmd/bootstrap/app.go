package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lechuhuuha/log_forge/config"
	"github.com/lechuhuuha/log_forge/internal/domain"
	httpapi "github.com/lechuhuuha/log_forge/internal/http"
	"github.com/lechuhuuha/log_forge/internal/profileutil"
	"github.com/lechuhuuha/log_forge/internal/queue"
	"github.com/lechuhuuha/log_forge/internal/storage"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/service"
	"github.com/lechuhuuha/log_forge/util"
)

// AppConfig holds the runtime options required to start the application.
type AppConfig struct {
	Addr                string
	Version             int
	LogsDir             string
	AnalyticsDir        string
	AggregationInterval time.Duration
	KafkaSettings       *config.KafkaSettings
	Ingestion           config.IngestionSettings
	Consumer            config.ConsumerSettings
}

// App wires together the HTTP server, services, and background workers.
type App struct {
	cfg    AppConfig
	logger loggerpkg.Logger
}

// BuildApp loads any file-based configuration overrides and returns a ready-to-run App.
func BuildApp(cliCfg config.CLIConfig, logger loggerpkg.Logger) (*App, error) {
	if strings.TrimSpace(cliCfg.ConfigPath) == "" {
		return nil, errors.New("config file path is required")
	}

	fileCfg, err := config.Load(cliCfg.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("load config file: %w", err)
	}

	aggInterval, err := fileCfg.AggregationInterval(0)
	if err != nil {
		return nil, fmt.Errorf("invalid aggregation interval: %w", err)
	}

	appCfg := AppConfig{
		Addr:                fileCfg.Server.Addr,
		Version:             fileCfg.Version,
		LogsDir:             fileCfg.Storage.LogsDir,
		AnalyticsDir:        fileCfg.Storage.AnalyticsDir,
		AggregationInterval: aggInterval,
		KafkaSettings:       &fileCfg.Kafka,
		Ingestion:           fileCfg.Ingestion,
		Consumer:            fileCfg.Consumer,
	}

	return newApp(appCfg, logger)
}

// newApp returns a configured App instance.
func newApp(cfg AppConfig, logger loggerpkg.Logger) (*App, error) {
	if logger == nil {
		logger = loggerpkg.NewNop()
	}
	if cfg.AggregationInterval <= 0 {
		return nil, errors.New("aggregation interval must be positive")
	}
	return &App{cfg: cfg, logger: logger}, nil
}

// Run starts the HTTP server and supporting goroutines until the context is canceled.
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	run := func() error {
		producerWriteTimeout := a.cfg.Ingestion.ProducerWriteTimeout
		if producerWriteTimeout <= 0 {
			producerWriteTimeout = 10 * time.Second
		}
		consumerFlushInterval := a.cfg.Consumer.FlushInterval
		if consumerFlushInterval <= 0 {
			consumerFlushInterval = 500 * time.Millisecond
		}
		consumerPersistTimeout := a.cfg.Consumer.PersistTimeout
		if consumerPersistTimeout <= 0 {
			consumerPersistTimeout = 5 * time.Second
		}

		consumerBatchCfg := service.ConsumerBatchConfig{
			FlushSize:      a.cfg.Consumer.FlushSize,
			FlushInterval:  consumerFlushInterval,
			PersistTimeout: consumerPersistTimeout,
		}

		store := storage.NewFileLogStore(a.cfg.LogsDir, a.logger)
		var (
			queueImpl domain.LogQueue
			mode      service.PipelineMode = service.ModeDirect
			pipeline  *service.PipelineService
		)

		if a.cfg.Version == 2 {
			mode = service.ModeQueue
			if a.cfg.KafkaSettings == nil || len(a.cfg.KafkaSettings.Brokers) > 0 {
				return fmt.Errorf("kafka settings are required for version 2")
			}

			batchTimeout := a.cfg.KafkaSettings.BatchTimeout
			if batchTimeout <= 0 {
				batchTimeout = time.Second
			}
			kafkaQueue, err := queue.NewKafkaLogQueue(queue.KafkaConfig{
				Brokers:        a.cfg.KafkaSettings.Brokers,
				Topic:          a.cfg.KafkaSettings.Topic,
				GroupID:        a.cfg.KafkaSettings.GroupID,
				BatchSize:      a.cfg.KafkaSettings.BatchSize,
				BatchTimeout:   batchTimeout,
				Consumers:      a.cfg.KafkaSettings.Consumers,
				RequireAllAcks: a.cfg.KafkaSettings.RequireAllAcks,
				BatchBytes:     a.cfg.KafkaSettings.BatchBytes,
				Compression:    a.cfg.KafkaSettings.Compression,
				Async:          a.cfg.KafkaSettings.Async,
			}, a.logger)
			if err != nil {
				return fmt.Errorf("configure kafka: %w", err)
			}

			queueImpl = kafkaQueue
			pipeline = service.NewPipelineService(queueImpl, store, consumerBatchCfg, a.logger, kafkaQueue.Close)
			if err := pipeline.Start(ctx); err != nil {
				return fmt.Errorf("start kafka consumers: %w", err)
			}
			defer pipeline.Close()
		} else {
			queueImpl = queue.NoopQueue{}
		}

		var producerSvc *service.ProducerService
		if mode == service.ModeQueue {
			producerCfg := &service.ProducerConfig{
				QueueBufferSize:       a.cfg.Ingestion.QueueBufferSize,
				Workers:               a.cfg.Ingestion.ProducerWorkers,
				WriteTimeout:          producerWriteTimeout,
				QueueHighWaterPercent: a.cfg.Ingestion.QueueHighWaterPercent,
			}
			producerSvc = service.NewProducerService(queueImpl, a.logger, producerCfg)
			producerSvc.Start()
			defer producerSvc.Close()
		}

		ingestionSvc := service.NewIngestionService(store, producerSvc, mode, a.logger)
		defer ingestionSvc.Close()

		aggSvc := service.NewAggregationService(a.cfg.LogsDir, a.cfg.AnalyticsDir, a.cfg.AggregationInterval, a.logger)
		aggSvc.Start(ctx)

		mux := http.NewServeMux()
		handler := httpapi.NewHandler(ingestionSvc, a.logger)
		handler.RegisterRoutes(mux)
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})

		server := &http.Server{
			Addr:    a.cfg.Addr,
			Handler: mux,
		}

		go func() {
			a.logger.Info("server listening", loggerpkg.F("addr", a.cfg.Addr), loggerpkg.F("version", a.cfg.Version))
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				a.logger.Fatal("server error", loggerpkg.F("error", err))
			}
		}()

		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			a.logger.Warn("graceful shutdown failed", loggerpkg.F("error", err))
		}
		return nil
	}

	if util.CaptureProfiles() {
		profileName := util.GetEnv(util.ProfileName, fmt.Sprintf("version%d", a.cfg.Version))
		dir := util.GetEnv(util.ProfileDir, util.DefaultProfileDir)
		a.logger.Info("profiling enabled", loggerpkg.F("dir", dir), loggerpkg.F("profile", profileName))
		if err := profileutil.WithProfiling(dir, profileName, a.logger, run); err != nil {
			return err
		}
		return nil
	}

	return run()
}
