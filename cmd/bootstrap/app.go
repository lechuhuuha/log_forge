package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/lechuhuuha/log_forge/internal/config"
	"github.com/lechuhuuha/log_forge/internal/domain"
	httpapi "github.com/lechuhuuha/log_forge/internal/http"
	"github.com/lechuhuuha/log_forge/internal/profileutil"
	"github.com/lechuhuuha/log_forge/internal/queue"
	"github.com/lechuhuuha/log_forge/internal/service"
	"github.com/lechuhuuha/log_forge/internal/storage"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
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
}

// App wires together the HTTP server, services, and background workers.
type App struct {
	cfg    AppConfig
	logger loggerpkg.Logger
}

// BuildApp loads any file-based configuration overrides and returns a ready-to-run App.
func BuildApp(cliCfg CLIConfig, logger loggerpkg.Logger) (*App, error) {
	appCfg := AppConfig{
		Addr:                cliCfg.Addr,
		Version:             cliCfg.Version,
		LogsDir:             cliCfg.LogsDir,
		AnalyticsDir:        cliCfg.AnalyticsDir,
		AggregationInterval: cliCfg.AggregationInterval,
	}

	if cliCfg.ConfigPath != "" {
		fileCfg, err := config.Load(cliCfg.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("load config file: %w", err)
		}
		appCfg.Version = fileCfg.Version
		appCfg.Addr = fileCfg.Server.Addr
		appCfg.LogsDir = fileCfg.Storage.LogsDir
		appCfg.AnalyticsDir = fileCfg.Storage.AnalyticsDir
		aggInterval, err := fileCfg.AggregationInterval(cliCfg.AggregationInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid aggregation interval: %w", err)
		}
		appCfg.AggregationInterval = aggInterval
		appCfg.KafkaSettings = &fileCfg.Kafka
	}

	return NewApp(appCfg, logger)
}

// NewApp returns a configured App instance.
func NewApp(cfg AppConfig, logger loggerpkg.Logger) (*App, error) {
	if logger == nil {
		logger = loggerpkg.NewNop()
	}
	if cfg.AggregationInterval <= 0 {
		return nil, errors.New("aggregation interval must be positive")
	}
	if cfg.Version == 0 {
		cfg.Version = util.DefaultPipelineVersion
	}
	if cfg.Addr == "" {
		cfg.Addr = util.DefaultHTTPAddr
	}
	if cfg.LogsDir == "" {
		cfg.LogsDir = util.DefaultLogsDir
	}
	if cfg.AnalyticsDir == "" {
		cfg.AnalyticsDir = util.DefaultAnalyticsDir
	}
	return &App{cfg: cfg, logger: logger}, nil
}

// Run starts the HTTP server and supporting goroutines until the context is canceled.
func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	run := func() error {
		store := storage.NewFileLogStore(a.cfg.LogsDir, a.logger)
		var (
			queueImpl domain.LogQueue
			mode      service.PipelineMode = service.ModeDirect
			closer    func() error
		)

		var batchWriter *consumerBatchWriter

		if a.cfg.Version == 2 {
			mode = service.ModeQueue
			var (
				kafkaQueue *queue.KafkaLogQueue
				err        error
			)
			if a.cfg.KafkaSettings != nil && len(a.cfg.KafkaSettings.Brokers) > 0 {
				batchTimeout, err := a.cfg.KafkaSettings.KafkaBatchTimeout(time.Second)
				if err != nil {
					return fmt.Errorf("invalid kafka batch timeout: %w", err)
				}
				kafkaQueue, err = queue.NewKafkaLogQueue(queue.KafkaConfig{
					Brokers:        a.cfg.KafkaSettings.Brokers,
					Topic:          a.cfg.KafkaSettings.Topic,
					GroupID:        a.cfg.KafkaSettings.GroupID,
					BatchSize:      a.cfg.KafkaSettings.BatchSize,
					BatchTimeout:   batchTimeout,
					Consumers:      a.cfg.KafkaSettings.Consumers,
					RequireAllAcks: a.cfg.KafkaSettings.RequireAllAcks,
				}, a.logger)
				if err != nil {
					return fmt.Errorf("configure kafka: %w", err)
				}
			} else {
				kafkaQueue, err = util.CreateKafkaQueueFromEnv(a.logger)
				if err != nil {
					return fmt.Errorf("configure kafka: %w", err)
				}
			}
			queueImpl = kafkaQueue
			closer = kafkaQueue.Close

			consumeCtx, consumeCancel := context.WithCancel(ctx)
			batchWriter = newConsumerBatchWriter(consumeCtx, store, 0, 0, a.logger)
			defer func() {
				consumeCancel()
				batchWriter.Close()
			}()

			if err := kafkaQueue.StartConsumers(consumeCtx, func(c context.Context, rec domain.LogRecord) {
				batchWriter.Add(rec)
			}); err != nil {
				return fmt.Errorf("start kafka consumers: %w", err)
			}
		} else {
			queueImpl = queue.NoopQueue{}
		}

		if closer != nil {
			defer closer()
		}

		ingestionSvc := service.NewIngestionService(store, queueImpl, mode, a.logger)
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
