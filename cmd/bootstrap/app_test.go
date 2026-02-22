package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/config"
	"github.com/lechuhuuha/log_forge/util"
)

func TestNewApp(t *testing.T) {
	cases := []struct {
		name        string
		cliCfg      config.CLIConfig
		wantErr     bool
		errContains string
		check       func(t *testing.T, app *App)
	}{
		{
			name:        "missing config path",
			cliCfg:      config.CLIConfig{},
			wantErr:     true,
			errContains: "config file path is required",
		},
		{
			name: "invalid aggregation interval",
			cliCfg: func() config.CLIConfig {
				return config.CLIConfig{ConfigPath: writeConfigFile(t, 1, "not-a-duration", nil)}
			}(),
			wantErr:     true,
			errContains: "invalid aggregation interval",
		},
		{
			name: "invalid storage backend",
			cliCfg: func() config.CLIConfig {
				return config.CLIConfig{
					ConfigPath: writeConfigFileWithPathsAndOptions(
						t,
						1,
						"1s",
						nil,
						filepath.ToSlash(filepath.Join(t.TempDir(), "logs")),
						filepath.ToSlash(filepath.Join(t.TempDir(), "analytics")),
						testConfigOptions{
							StorageBackend: "invalid",
						},
					),
				}
			}(),
			wantErr:     true,
			errContains: "invalid storage backend",
		},
		{
			name: "loads valid config",
			cliCfg: func() config.CLIConfig {
				return config.CLIConfig{ConfigPath: writeConfigFile(t, 1, "1s", nil)}
			}(),
			check: func(t *testing.T, app *App) {
				if app.cfg.Version != 1 {
					t.Fatalf("expected version 1, got %d", app.cfg.Version)
				}
				if app.cfg.Addr != ":8082" {
					t.Fatalf("expected addr :8082, got %q", app.cfg.Addr)
				}
				if app.buildInfo.Version != "dev" {
					t.Fatalf("expected default build version dev, got %q", app.buildInfo.Version)
				}
				if app.buildInfo.Commit != "none" {
					t.Fatalf("expected default commit none, got %q", app.buildInfo.Commit)
				}
				if app.buildInfo.BuildDate != "unknown" {
					t.Fatalf("expected default build date unknown, got %q", app.buildInfo.BuildDate)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			app, err := NewApp(tc.cliCfg, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error containing %q, got %v", tc.errContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewApp returned error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, app)
			}
		})
	}
}

func TestNewAppWithBuildInfo(t *testing.T) {
	cases := []struct {
		name     string
		build    BuildInfo
		expected BuildInfo
	}{
		{
			name:     "empty build info uses defaults",
			build:    BuildInfo{},
			expected: BuildInfo{Version: "dev", Commit: "none", BuildDate: "unknown"},
		},
		{
			name: "custom build info is preserved",
			build: BuildInfo{
				Version:   "v0.1.0",
				Commit:    "abcd1234",
				BuildDate: "2026-02-22T00:00:00Z",
			},
			expected: BuildInfo{
				Version:   "v0.1.0",
				Commit:    "abcd1234",
				BuildDate: "2026-02-22T00:00:00Z",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			app, err := NewAppWithBuildInfo(
				config.CLIConfig{ConfigPath: writeConfigFile(t, 1, "1s", nil)},
				nil,
				tc.build,
			)
			if err != nil {
				t.Fatalf("NewAppWithBuildInfo returned error: %v", err)
			}
			if app.buildInfo != tc.expected {
				t.Fatalf("unexpected build info: got=%+v want=%+v", app.buildInfo, tc.expected)
			}
		})
	}
}

func TestBuildApp(t *testing.T) {
	cases := []struct {
		name        string
		version     int
		interval    string
		brokers     []string
		useNilCtx   bool
		wantErr     bool
		errContains string
		check       func(t *testing.T, server *http.Server)
	}{
		{
			name:     "version1 routes",
			version:  1,
			interval: "1s",
			brokers:  nil,
			check: func(t *testing.T, server *http.Server) {
				reqCases := []struct {
					name   string
					method string
					path   string
					want   int
					check  func(t *testing.T, rec *httptest.ResponseRecorder)
				}{
					{name: "health", method: http.MethodGet, path: "/health", want: http.StatusOK},
					{name: "ready", method: http.MethodGet, path: "/ready", want: http.StatusOK},
					{name: "ready method not allowed", method: http.MethodPost, path: "/ready", want: http.StatusMethodNotAllowed},
					{
						name:   "version",
						method: http.MethodGet,
						path:   "/version",
						want:   http.StatusOK,
						check: func(t *testing.T, rec *httptest.ResponseRecorder) {
							var body struct {
								Version      string `json:"version"`
								Commit       string `json:"commit"`
								BuildDate    string `json:"buildDate"`
								PipelineMode int    `json:"pipelineMode"`
							}
							if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
								t.Fatalf("decode /version response: %v", err)
							}
							if body.Version != "dev" || body.Commit != "none" || body.BuildDate != "unknown" {
								t.Fatalf("unexpected /version metadata: %+v", body)
							}
							if body.PipelineMode != 1 {
								t.Fatalf("expected pipeline mode 1, got %d", body.PipelineMode)
							}
						},
					},
					{name: "version method not allowed", method: http.MethodPost, path: "/version", want: http.StatusMethodNotAllowed},
					{name: "metrics", method: http.MethodGet, path: "/metrics", want: http.StatusOK},
					{name: "logs method not allowed", method: http.MethodGet, path: "/logs", want: http.StatusMethodNotAllowed},
				}

				for _, rc := range reqCases {
					rc := rc
					t.Run(rc.name, func(t *testing.T) {
						rec := httptest.NewRecorder()
						req := httptest.NewRequest(rc.method, rc.path, nil)
						server.Handler.ServeHTTP(rec, req)
						if rec.Code != rc.want {
							t.Fatalf("unexpected status for %s %s: got=%d want=%d", rc.method, rc.path, rec.Code, rc.want)
						}
						if rc.check != nil {
							rc.check(t, rec)
						}
					})
				}
			},
		},
		{
			name:        "version2 requires kafka brokers",
			version:     2,
			interval:    "1s",
			brokers:     nil,
			wantErr:     true,
			errContains: "kafka settings are required for version 2",
		},
		{
			name:        "version2 startup check fails when broker is unreachable",
			version:     2,
			interval:    "1s",
			brokers:     []string{"127.0.0.1:1"},
			wantErr:     true,
			errContains: "kafka startup check",
		},
		{
			name:      "nil context still builds",
			version:   1,
			interval:  "1s",
			useNilCtx: true,
			check: func(t *testing.T, server *http.Server) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/health", nil)
				server.Handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("expected /health 200, got %d", rec.Code)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := writeConfigFile(t, tc.version, tc.interval, tc.brokers)
			app, err := NewApp(config.CLIConfig{ConfigPath: cfgPath}, nil)
			if err != nil {
				t.Fatalf("NewApp returned error: %v", err)
			}

			var ctx context.Context
			var cancel context.CancelFunc
			if tc.useNilCtx {
				ctx = nil
				cancel = func() {}
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()

			server, cleanup, err := app.BuildApp(ctx)
			if tc.wantErr {
				if cleanup != nil {
					cleanup()
				}
				if err == nil {
					t.Fatal("expected error")
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Fatalf("expected error containing %q, got %v", tc.errContains, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildApp returned error: %v", err)
			}
			defer cleanup()

			if tc.check != nil {
				tc.check(t, server)
			}
		})
	}
}

func TestBuildAppVersion2ReachableBroker(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start broker stub: %v", err)
	}
	defer listener.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	cfgPath := writeConfigFile(t, 2, "1s", []string{listener.Addr().String()})
	app, err := NewApp(config.CLIConfig{ConfigPath: cfgPath}, nil)
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	server, cleanup, err := app.BuildApp(ctx)
	if err != nil {
		cancel()
		t.Fatalf("BuildApp returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		cancel()
		cleanup()
		t.Fatalf("expected /health 200, got %d", rec.Code)
	}

	readyRec := httptest.NewRecorder()
	readyReq := httptest.NewRequest(http.MethodGet, "/ready", nil)
	server.Handler.ServeHTTP(readyRec, readyReq)
	if readyRec.Code != http.StatusOK {
		cancel()
		cleanup()
		t.Fatalf("expected /ready 200 while broker reachable, got %d", readyRec.Code)
	}

	versionRec := httptest.NewRecorder()
	versionReq := httptest.NewRequest(http.MethodGet, "/version", nil)
	server.Handler.ServeHTTP(versionRec, versionReq)
	if versionRec.Code != http.StatusOK {
		cancel()
		cleanup()
		t.Fatalf("expected /version 200, got %d", versionRec.Code)
	}
	var versionBody struct {
		PipelineMode int `json:"pipelineMode"`
	}
	if err := json.Unmarshal(versionRec.Body.Bytes(), &versionBody); err != nil {
		cancel()
		cleanup()
		t.Fatalf("decode /version response: %v", err)
	}
	if versionBody.PipelineMode != 2 {
		cancel()
		cleanup()
		t.Fatalf("expected /version pipelineMode 2, got %d", versionBody.PipelineMode)
	}

	_ = listener.Close()
	<-done

	readyAfterCloseRec := httptest.NewRecorder()
	readyAfterCloseReq := httptest.NewRequest(http.MethodGet, "/ready", nil)
	server.Handler.ServeHTTP(readyAfterCloseRec, readyAfterCloseReq)
	if readyAfterCloseRec.Code != http.StatusServiceUnavailable {
		cancel()
		cleanup()
		t.Fatalf("expected /ready 503 after broker close, got %d", readyAfterCloseRec.Code)
	}

	cancel()
	cleanup()
}

func TestBuildAppReadyFailsWhenStoragePathIsFile(t *testing.T) {
	dir := t.TempDir()
	logsPath := filepath.Join(dir, "logs-file")
	if err := os.WriteFile(logsPath, []byte("not-a-directory"), 0o644); err != nil {
		t.Fatalf("write logs path file: %v", err)
	}
	cfgPath := writeConfigFileWithPaths(t, 1, "1s", nil, logsPath, filepath.Join(dir, "analytics"))
	app, err := NewApp(config.CLIConfig{ConfigPath: cfgPath}, nil)
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}

	server, cleanup, err := app.BuildApp(context.Background())
	if err != nil {
		t.Fatalf("BuildApp returned error: %v", err)
	}
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected /ready 503, got %d", rec.Code)
	}
}

func TestBuildAppMinIOStartup(t *testing.T) {
	cfgPath := writeConfigFileWithPathsAndOptions(
		t,
		1,
		"1s",
		nil,
		filepath.ToSlash(filepath.Join(t.TempDir(), "logs")),
		filepath.ToSlash(filepath.Join(t.TempDir(), "analytics")),
		testConfigOptions{
			StorageBackend:  config.StorageBackendMinIO,
			MinIOEndpoint:   "127.0.0.1:1",
			MinIOBucket:     "logs-bucket",
			MinIOAccessKey:  "minio-access",
			MinIOSecretKey:  "minio-secret",
			MinIOLogsPrefix: "logs",
		},
	)
	app, err := NewApp(config.CLIConfig{ConfigPath: cfgPath}, nil)
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}

	server, cleanup, err := app.BuildApp(context.Background())
	if err != nil {
		t.Fatalf("BuildApp returned error: %v", err)
	}
	defer cleanup()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected /health 200, got %d", rec.Code)
	}
}

func TestBuildAppUnsupportedStorageBackend(t *testing.T) {
	cfgPath := writeConfigFile(t, 1, "1s", nil)
	app, err := NewApp(config.CLIConfig{ConfigPath: cfgPath}, nil)
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}
	app.cfg.StorageBackend = config.StorageBackend("unknown")

	_, cleanup, err := app.BuildApp(context.Background())
	if cleanup != nil {
		cleanup()
	}
	if err == nil {
		t.Fatal("expected unsupported storage backend error")
	}
	if !strings.Contains(err.Error(), "unsupported storage backend") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildAppAppliesAPIKeyAuthConfig(t *testing.T) {
	cfgPath := writeConfigFileWithPathsAndOptions(
		t,
		1,
		"1s",
		nil,
		filepath.ToSlash(filepath.Join(t.TempDir(), "logs")),
		filepath.ToSlash(filepath.Join(t.TempDir(), "analytics")),
		testConfigOptions{
			AuthEnabled: true,
			AuthHeader:  "X-Lab-Key",
			AuthKeys:    []string{"secret-key"},
		},
	)
	app, err := NewApp(config.CLIConfig{ConfigPath: cfgPath}, nil)
	if err != nil {
		t.Fatalf("NewApp returned error: %v", err)
	}

	server, cleanup, err := app.BuildApp(context.Background())
	if err != nil {
		t.Fatalf("BuildApp returned error: %v", err)
	}
	defer cleanup()

	body := []byte(`[{"timestamp":"2026-02-22T00:00:00Z","path":"/home","userAgent":"ua"}]`)

	unauthorized := httptest.NewRecorder()
	unauthorizedReq := httptest.NewRequest(http.MethodPost, "/logs", bytes.NewReader(body))
	unauthorizedReq.Header.Set("Content-Type", "application/json")
	server.Handler.ServeHTTP(unauthorized, unauthorizedReq)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized /logs status 401, got %d", unauthorized.Code)
	}

	authorized := httptest.NewRecorder()
	authorizedReq := httptest.NewRequest(http.MethodPost, "/logs", bytes.NewReader(body))
	authorizedReq.Header.Set("Content-Type", "application/json")
	authorizedReq.Header.Set("X-Lab-Key", "secret-key")
	server.Handler.ServeHTTP(authorized, authorizedReq)
	if authorized.Code != http.StatusAccepted {
		t.Fatalf("expected authorized /logs status 202, got %d", authorized.Code)
	}
}

func TestRun(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T)
		ctx   func() (context.Context, context.CancelFunc)
		check func(t *testing.T)
	}{
		{
			name:  "canceled context returns nil",
			setup: func(_ *testing.T) {},
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
		},
		{
			name: "profile capture path writes files",
			setup: func(t *testing.T) {
				profileDir := t.TempDir()
				t.Setenv(util.ProfileCapture, "1")
				t.Setenv(util.ProfileDir, profileDir)
				t.Setenv(util.ProfileName, "bootstrap_test")
				t.Setenv("BOOTSTRAP_TEST_PROFILE_DIR", profileDir)
			},
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, cancel
			},
			check: func(t *testing.T) {
				profileDir := os.Getenv("BOOTSTRAP_TEST_PROFILE_DIR")
				for _, name := range []string{
					"cpu_bootstrap_test.prof",
					"heap_bootstrap_test.prof",
					"goroutine_bootstrap_test.prof",
				} {
					if _, err := os.Stat(filepath.Join(profileDir, name)); err != nil {
						t.Fatalf("expected profile %s to exist: %v", name, err)
					}
				}
			},
		},
		{
			name:  "timeout context path",
			setup: func(_ *testing.T) {},
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				return ctx, cancel
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := writeConfigFile(t, 1, "1s", nil)
			app, err := NewApp(config.CLIConfig{ConfigPath: cfgPath}, nil)
			if err != nil {
				t.Fatalf("NewApp returned error: %v", err)
			}

			tc.setup(t)
			ctx, cancel := tc.ctx()
			defer cancel()
			if err := app.Run(ctx); err != nil {
				t.Fatalf("Run returned error: %v", err)
			}
			if tc.check != nil {
				tc.check(t)
			}
		})
	}
}

func TestInitWiring(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "InitLogger returns logger and cleanup",
			run: func(t *testing.T) {
				logr, cleanup, err := InitLogger()
				if err != nil {
					t.Fatalf("InitLogger returned error: %v", err)
				}
				if logr == nil {
					t.Fatal("expected non-nil logger")
				}
				cleanup()
			},
		},
		{
			name: "InitObservability with pprof disabled",
			run: func(t *testing.T) {
				t.Setenv(util.ProfileEnable, "")
				InitObservability(nil)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t)
		})
	}
}

type testConfigOptions struct {
	StorageBackend  config.StorageBackend
	MinIOEndpoint   string
	MinIOBucket     string
	MinIOAccessKey  string
	MinIOSecretKey  string
	MinIOUseSSL     bool
	MinIOLogsPrefix string
	AuthEnabled     bool
	AuthHeader      string
	AuthKeys        []string
}

func writeConfigFile(t *testing.T, version int, interval string, kafkaBrokers []string) string {
	t.Helper()
	dir := t.TempDir()
	logsDir := filepath.ToSlash(filepath.Join(dir, "logs"))
	analyticsDir := filepath.ToSlash(filepath.Join(dir, "analytics"))
	return writeConfigFileWithPaths(t, version, interval, kafkaBrokers, logsDir, analyticsDir)
}

func writeConfigFileWithPaths(
	t *testing.T,
	version int,
	interval string,
	kafkaBrokers []string,
	logsDir string,
	analyticsDir string,
) string {
	return writeConfigFileWithPathsAndOptions(t, version, interval, kafkaBrokers, logsDir, analyticsDir, testConfigOptions{})
}

func writeConfigFileWithPathsAndOptions(
	t *testing.T,
	version int,
	interval string,
	kafkaBrokers []string,
	logsDir string,
	analyticsDir string,
	opts testConfigOptions,
) string {
	t.Helper()
	cfgDir := t.TempDir()

	backend := opts.StorageBackend
	if backend == "" {
		backend = config.StorageBackendFile
	}
	authHeader := strings.TrimSpace(opts.AuthHeader)
	if authHeader == "" {
		authHeader = "X-API-Key"
	}

	brokersYAML := "[]"
	if len(kafkaBrokers) > 0 {
		parts := make([]string, 0, len(kafkaBrokers))
		for _, b := range kafkaBrokers {
			parts = append(parts, fmt.Sprintf("- %q", b))
		}
		brokersYAML = "\n    " + strings.Join(parts, "\n    ")
	}
	authKeysYAML := "[]"
	if len(opts.AuthKeys) > 0 {
		parts := make([]string, 0, len(opts.AuthKeys))
		for _, key := range opts.AuthKeys {
			parts = append(parts, fmt.Sprintf("- %q", key))
		}
		authKeysYAML = "\n    " + strings.Join(parts, "\n    ")
	}

	content := fmt.Sprintf(`version: %d
server:
  addr: ":8082"
storage:
  backend: %q
  logsDir: %q
  analyticsDir: %q
  minio:
    endpoint: %q
    bucket: %q
    accessKey: %q
    secretKey: %q
    useSSL: %t
    logsPrefix: %q
auth:
  enabled: %t
  headerName: %q
  keys: %s
aggregation:
  interval: %q
kafka:
  brokers: %s
  topic: "logs"
  groupID: "logs-consumer-group"
  batchSize: 100
  batchTimeout: 1s
  consumers: 1
  requireAllAcks: false`,
		version,
		backend,
		filepath.ToSlash(logsDir),
		filepath.ToSlash(analyticsDir),
		opts.MinIOEndpoint,
		opts.MinIOBucket,
		opts.MinIOAccessKey,
		opts.MinIOSecretKey,
		opts.MinIOUseSSL,
		opts.MinIOLogsPrefix,
		opts.AuthEnabled,
		authHeader,
		authKeysYAML,
		interval,
		brokersYAML,
	)

	path := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
