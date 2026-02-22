package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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
	// Addr is the HTTP listen address (for example, ":8082").
	Addr string
	// ReadHeaderTimeout limits how long the server reads request headers.
	ReadHeaderTimeout time.Duration
	// ReadTimeout limits the total time to read the entire request.
	ReadTimeout time.Duration
	// WriteTimeout limits the time to write a response.
	WriteTimeout time.Duration
	// IdleTimeout limits how long keep-alive connections stay idle.
	IdleTimeout time.Duration
	// RequestTimeout is the per-request processing timeout for /logs.
	RequestTimeout time.Duration
	// Version selects the pipeline mode (1=direct file write, 2=Kafka-backed).
	Version int
	// LogsDir is the base directory for raw hourly log files.
	LogsDir string
	// AnalyticsDir is the base directory for aggregated summary files.
	AnalyticsDir string
	// StorageBackend selects log persistence implementation (file|minio).
	StorageBackend config.StorageBackend
	// MinIO contains object-storage settings used when backend=minio.
	MinIO config.MinIOSettings
	// Auth contains API key authentication settings for /logs.
	Auth config.AuthSettings
	// AggregationInterval controls how often background aggregation runs.
	AggregationInterval time.Duration
	// KafkaSettings contains Kafka producer/consumer configuration for v2 mode.
	KafkaSettings *config.KafkaSettings
	// Ingestion contains producer-side buffering/retry/circuit configuration.
	Ingestion config.IngestionSettings
	// Consumer contains consumer-side flush and persistence settings.
	Consumer config.ConsumerSettings
}

// BuildInfo contains build-time metadata exposed by the runtime.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

func normalizeBuildInfo(info BuildInfo) BuildInfo {
	if strings.TrimSpace(info.Version) == "" {
		info.Version = "dev"
	}
	if strings.TrimSpace(info.Commit) == "" {
		info.Commit = "none"
	}
	if strings.TrimSpace(info.BuildDate) == "" {
		info.BuildDate = "unknown"
	}
	return info
}

func checkWritableDir(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("storage path is empty")
	}
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("storage path %q is not a directory", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat storage path %q: %w", path, err)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("ensure storage directory %q: %w", path, err)
	}
	f, err := os.CreateTemp(path, ".ready-*")
	if err != nil {
		return fmt.Errorf("create temp file in %q: %w", path, err)
	}
	tempPath := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(tempPath)
		return fmt.Errorf("close temp file in %q: %w", path, err)
	}
	if err := os.Remove(tempPath); err != nil {
		return fmt.Errorf("remove temp file %q: %w", tempPath, err)
	}
	return nil
}

func runReadinessChecks(ctx context.Context, checks ...func(context.Context) error) error {
	for _, check := range checks {
		if check == nil {
			continue
		}
		if err := check(ctx); err != nil {
			return err
		}
	}
	return nil
}

// App wires together the HTTP server, services, and background workers.
type App struct {
	cfg       AppConfig
	buildInfo BuildInfo
	logger    loggerpkg.Logger
}

// NewApp loads file-based configuration and returns a ready-to-run App.
func NewApp(cliCfg config.CLIConfig, logger loggerpkg.Logger) (*App, error) {
	return NewAppWithBuildInfo(cliCfg, logger, BuildInfo{})
}

// NewAppWithBuildInfo loads file-based configuration and injects build metadata.
func NewAppWithBuildInfo(cliCfg config.CLIConfig, logger loggerpkg.Logger, buildInfo BuildInfo) (*App, error) {
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
		ReadHeaderTimeout:   fileCfg.Server.ReadHeaderTimeout,
		ReadTimeout:         fileCfg.Server.ReadTimeout,
		WriteTimeout:        fileCfg.Server.WriteTimeout,
		IdleTimeout:         fileCfg.Server.IdleTimeout,
		RequestTimeout:      fileCfg.Server.RequestTimeout,
		Version:             fileCfg.Version,
		LogsDir:             fileCfg.Storage.LogsDir,
		AnalyticsDir:        fileCfg.Storage.AnalyticsDir,
		StorageBackend:      fileCfg.Storage.Backend,
		MinIO:               fileCfg.Storage.MinIO,
		Auth:                fileCfg.Auth,
		AggregationInterval: aggInterval,
		KafkaSettings:       &fileCfg.Kafka,
		Ingestion:           fileCfg.Ingestion,
		Consumer:            fileCfg.Consumer,
	}

	return &App{cfg: appCfg, buildInfo: normalizeBuildInfo(buildInfo), logger: logger}, nil
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

	var (
		storeRepo      repo.Repository
		minioStoreRepo *repo.MinIORepo
		readyChecks    []func(context.Context) error
		storageBackend = a.cfg.StorageBackend
	)
	if storageBackend == "" {
		storageBackend = config.StorageBackendFile
	}
	readyChecks = append(readyChecks, func(context.Context) error { return checkWritableDir(a.cfg.AnalyticsDir) })
	switch storageBackend {
	case config.StorageBackendFile:
		storeRepo = repo.NewFileRepo(a.cfg.LogsDir)
		readyChecks = append(readyChecks, func(context.Context) error { return checkWritableDir(a.cfg.LogsDir) })
	case config.StorageBackendMinIO:
		minioRepo, err := repo.NewMinIORepo(repo.MinIORepoOptions{
			Endpoint:   a.cfg.MinIO.Endpoint,
			Bucket:     a.cfg.MinIO.Bucket,
			AccessKey:  a.cfg.MinIO.AccessKey,
			SecretKey:  a.cfg.MinIO.SecretKey,
			UseSSL:     a.cfg.MinIO.UseSSL,
			LogsPrefix: a.cfg.MinIO.LogsPrefix,
		})
		if err != nil {
			return nil, runCleanups, fmt.Errorf("configure minio repo: %w", err)
		}
		storeRepo = minioRepo
		minioStoreRepo = minioRepo
		readyChecks = append(readyChecks, minioRepo.CheckReady)
	default:
		return nil, runCleanups, fmt.Errorf("unsupported storage backend %q", storageBackend)
	}
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
		checkCtx, checkCancel := context.WithTimeout(ctx, util.KafkaStartupCheckTimeout)
		defer checkCancel()
		if err := logQueue.CheckConnectivity(checkCtx); err != nil {
			return nil, runCleanups, fmt.Errorf("kafka startup check: %w", err)
		}
		readyChecks = append(readyChecks, logQueue.CheckConnectivity)

		consumerSvc := service.NewConsumerService(logQueue, storeRepo, consumerBatchCfg, a.logger, logQueue.Close)
		if err := consumerSvc.Start(ctx); err != nil {
			return nil, runCleanups, fmt.Errorf("start kafka consumers: %w", err)
		}
		cleanups = append(cleanups, func() { consumerSvc.Close() })

		producerCfg := &service.ProducerConfig{
			QueueBufferSize:         a.cfg.Ingestion.QueueBufferSize,
			Workers:                 a.cfg.Ingestion.ProducerWorkers,
			WriteTimeout:            a.cfg.Ingestion.ProducerWriteTimeout,
			QueueHighWaterPercent:   a.cfg.Ingestion.QueueHighWaterPercent,
			CircuitFailureThreshold: a.cfg.Ingestion.CircuitFailureThreshold,
			CircuitCooldown:         a.cfg.Ingestion.CircuitCooldown,
		}
		producerSvc = service.NewProducerService(logQueue, a.logger, producerCfg)
		if !a.cfg.Ingestion.SyncOnIngest {
			producerSvc.StartAsync()
		}
		cleanups = append(cleanups, func() { producerSvc.Close() })
	}

	if minioStoreRepo != nil {
		minioAggSvc := service.NewMinIOAggregationService(minioStoreRepo, a.cfg.AnalyticsDir, a.cfg.AggregationInterval, a.logger)
		minioAggSvc.Start(ctx)
	} else {
		aggSvc := service.NewAggregationService(a.cfg.LogsDir, a.cfg.AnalyticsDir, a.cfg.AggregationInterval, a.logger)
		aggSvc.Start(ctx)
	}

	ingestionSvc := service.NewIngestionService(storeRepo, producerSvc, mode, a.cfg.Ingestion.SyncOnIngest, a.logger)
	cleanups = append(cleanups, func() { ingestionSvc.Close() })

	mux := http.NewServeMux()
	handler := httpapi.NewHandler(ingestionSvc, a.logger).
		WithRequestTimeout(a.cfg.RequestTimeout).
		WithAPIKeyAuth(a.cfg.Auth.Enabled, a.cfg.Auth.HeaderName, a.cfg.Auth.Keys)
	handler.RegisterRoutes(mux)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		readyCtx, cancel := context.WithTimeout(r.Context(), util.KafkaStartupCheckTimeout)
		defer cancel()
		if err := runReadinessChecks(readyCtx, readyChecks...); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Version      string `json:"version"`
			Commit       string `json:"commit"`
			BuildDate    string `json:"buildDate"`
			PipelineMode int    `json:"pipelineMode"`
		}{
			Version:      a.buildInfo.Version,
			Commit:       a.buildInfo.Commit,
			BuildDate:    a.buildInfo.BuildDate,
			PipelineMode: a.cfg.Version,
		})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:              a.cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: a.cfg.ReadHeaderTimeout,
		ReadTimeout:       a.cfg.ReadTimeout,
		WriteTimeout:      a.cfg.WriteTimeout,
		IdleTimeout:       a.cfg.IdleTimeout,
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
