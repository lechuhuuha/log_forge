package bootstrap

import (
	"context"
	"fmt"
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
				}{
					{name: "health", method: http.MethodGet, path: "/health", want: http.StatusOK},
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

func TestRun(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T)
		ctx   func() context.Context
		check func(t *testing.T)
	}{
		{
			name:  "canceled context returns nil",
			setup: func(_ *testing.T) {},
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
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
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
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
			ctx: func() context.Context {
				ctx, _ := context.WithTimeout(context.Background(), 10*time.Millisecond)
				return ctx
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := writeConfigFile(t, 1, "1s", nil)
			app, err := NewApp(config.CLIConfig{ConfigPath: cfgPath}, nil)
			if err != nil {
				t.Fatalf("NewApp returned error: %v", err)
			}

			tc.setup(t)
			if err := app.Run(tc.ctx()); err != nil {
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

func writeConfigFile(t *testing.T, version int, interval string, kafkaBrokers []string) string {
	t.Helper()
	dir := t.TempDir()
	logsDir := filepath.ToSlash(filepath.Join(dir, "logs"))
	analyticsDir := filepath.ToSlash(filepath.Join(dir, "analytics"))

	brokersYAML := "[]"
	if len(kafkaBrokers) > 0 {
		parts := make([]string, 0, len(kafkaBrokers))
		for _, b := range kafkaBrokers {
			parts = append(parts, fmt.Sprintf("- %q", b))
		}
		brokersYAML = "\n    " + strings.Join(parts, "\n    ")
	}

	content := fmt.Sprintf(`version: %d
server:
  addr: ":8082"
storage:
  logsDir: %q
  analyticsDir: %q
aggregation:
  interval: %q
kafka:
  brokers: %s
  topic: "logs"
  groupID: "logs-consumer-group"
  batchSize: 100
  batchTimeout: 1s
  consumers: 1
  requireAllAcks: false
`, version, logsDir, analyticsDir, interval, brokersYAML)

	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
