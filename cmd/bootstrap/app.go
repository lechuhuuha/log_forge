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
	httpapi "github.com/lechuhuuha/log_forge/internal/http"
	"github.com/lechuhuuha/log_forge/internal/queue"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/repo"
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

// NewApp loads file-based configuration and returns a ready-to-run App.
func NewApp(cliCfg config.CLIConfig, logger loggerpkg.Logger) (*App, error) {
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

	if aggInterval <= 0 {
		return nil, errors.New("aggregation interval must be positive")
	}

	if logger == nil {
		logger = loggerpkg.NewNop()
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

	return &App{cfg: appCfg, logger: logger}, nil
}

// BuildApp assembles services and the HTTP server, returning the server and a cleanup function.
func (a *App) BuildApp(ctx context.Context) (*http.Server, func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var cleanups []func()
	runCleanups := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	consumerBatchCfg := service.ConsumerBatchConfig{
		FlushSize:      a.cfg.Consumer.FlushSize,
		FlushInterval:  a.cfg.Consumer.FlushInterval,
		PersistTimeout: a.cfg.Consumer.PersistTimeout,
	}

	storeRepo := repo.NewFileRepo(a.cfg.LogsDir, a.logger)
	var (
		mode        service.PipelineMode = service.ModeDirect
		producerSvc *service.ProducerService
	)

	if a.cfg.Version == 2 {
		mode = service.ModeQueue
		if a.cfg.KafkaSettings == nil || len(a.cfg.KafkaSettings.Brokers) == 0 {
			return nil, runCleanups, fmt.Errorf("kafka settings are required for version 2")
		}

		batchTimeout := a.cfg.KafkaSettings.BatchTimeout
		if batchTimeout <= 0 {
			batchTimeout = time.Second
		}
		logQueue, err := queue.NewKafkaLogQueue(config.KafkaSettings{
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
			return nil, runCleanups, fmt.Errorf("configure kafka: %w", err)
		}

		consumerSvc := service.NewConsumerService(logQueue, storeRepo, consumerBatchCfg, a.logger, logQueue.Close)
		if err := consumerSvc.Start(ctx); err != nil {
			return nil, runCleanups, fmt.Errorf("start kafka consumers: %w", err)
		}
		cleanups = append(cleanups, func() { consumerSvc.Close() })

		producerCfg := &service.ProducerConfig{
			QueueBufferSize:       a.cfg.Ingestion.QueueBufferSize,
			Workers:               a.cfg.Ingestion.ProducerWorkers,
			WriteTimeout:          a.cfg.Ingestion.ProducerWriteTimeout,
			QueueHighWaterPercent: a.cfg.Ingestion.QueueHighWaterPercent,
		}
		producerSvc = service.NewProducerService(logQueue, a.logger, producerCfg)
		producerSvc.Start()
		cleanups = append(cleanups, func() { producerSvc.Close() })
	}

	aggSvc := service.NewAggregationService(a.cfg.LogsDir, a.cfg.AnalyticsDir, a.cfg.AggregationInterval, a.logger)
	aggSvc.Start(ctx)

	ingestionSvc := service.NewIngestionService(storeRepo, producerSvc, mode, a.logger)
	cleanups = append(cleanups, func() { ingestionSvc.Close() })

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

	return server, runCleanups, nil
}

// Run starts the HTTP server and supporting goroutines until the context is canceled.
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	run := func() error {
		server, cleanup, err := a.BuildApp(ctx)
		if err != nil {
			return err
		}
		defer cleanup()

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
		if err := util.WithProfiling(dir, profileName, a.logger, run); err != nil {
			return err
		}
		return nil
	}

	return run()
}
