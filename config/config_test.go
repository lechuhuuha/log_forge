package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withTestFlags(t *testing.T, args []string) {
	t.Helper()
	oldArgs := os.Args
	oldFS := flag.CommandLine
	t.Cleanup(func() {
		os.Args = oldArgs
		flag.CommandLine = oldFS
	})
	os.Args = args
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(os.Stderr)
}

func TestParseFlags(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "trims config path",
			args: []string{"server", "-config", "  config/examples/config.v1.local.yaml  "},
			want: "config/examples/config.v1.local.yaml",
		},
		{
			name: "empty config path",
			args: []string{"server"},
			want: "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			withTestFlags(t, tc.args)
			cfg := ParseFlags()
			if cfg.ConfigPath != tc.want {
				t.Fatalf("unexpected config path: got=%q want=%q", cfg.ConfigPath, tc.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T) string
		assertion func(t *testing.T, cfg *Config, err error)
	}{
		{
			name: "applies defaults for empty config",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				path := filepath.Join(dir, "config.yaml")
				if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
					t.Fatalf("write config: %v", err)
				}
				return path
			},
			assertion: func(t *testing.T, cfg *Config, err error) {
				if err != nil {
					t.Fatalf("Load returned error: %v", err)
				}
				if cfg.Version != 1 {
					t.Fatalf("expected default version 1, got %d", cfg.Version)
				}
				if cfg.Server.Addr != ":8082" {
					t.Fatalf("expected default addr :8082, got %q", cfg.Server.Addr)
				}
				if cfg.Storage.LogsDir != "logs" || cfg.Storage.AnalyticsDir != "analytics" {
					t.Fatalf("unexpected default storage dirs: logs=%q analytics=%q", cfg.Storage.LogsDir, cfg.Storage.AnalyticsDir)
				}
				if cfg.Storage.Backend != StorageBackendFile {
					t.Fatalf("expected default storage backend %q, got %q", StorageBackendFile, cfg.Storage.Backend)
				}
				if cfg.Storage.MinIO.LogsPrefix != "logs" || cfg.Storage.MinIO.AnalyticsPrefix != "analytics" {
					t.Fatalf(
						"unexpected default minio prefixes: logs=%q analytics=%q",
						cfg.Storage.MinIO.LogsPrefix,
						cfg.Storage.MinIO.AnalyticsPrefix,
					)
				}
				if cfg.Auth.Enabled {
					t.Fatal("expected auth.enabled to default to false")
				}
				if cfg.Auth.HeaderName != defaultAuthHeaderName {
					t.Fatalf("expected default auth.headerName %q, got %q", defaultAuthHeaderName, cfg.Auth.HeaderName)
				}
				if len(cfg.Auth.Keys) != 0 {
					t.Fatalf("expected empty default auth.keys, got %v", cfg.Auth.Keys)
				}
				if cfg.Aggregation.Interval != "1m" {
					t.Fatalf("expected default aggregation interval 1m, got %q", cfg.Aggregation.Interval)
				}
				if cfg.Kafka.Topic != "logs" || cfg.Kafka.GroupID != "logs-consumer-group" {
					t.Fatalf("unexpected default kafka config: topic=%q groupID=%q", cfg.Kafka.Topic, cfg.Kafka.GroupID)
				}
			},
		},
		{
			name: "parses custom auth and minio settings",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				path := filepath.Join(dir, "config.yaml")
				content := `
version: 2
auth:
  enabled: true
  headerName: "X-Lab-Key"
  keys:
    - " key-a "
    - ""
storage:
  backend: minio
  minio:
    endpoint: minio.storage.svc.cluster.local:9000
    bucket: logs-data
    accessKey: minio-access
    secretKey: minio-secret
    useSSL: true
    logsPrefix: ingest
    analyticsPrefix: summary
`
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatalf("write config: %v", err)
				}
				return path
			},
			assertion: func(t *testing.T, cfg *Config, err error) {
				if err != nil {
					t.Fatalf("Load returned error: %v", err)
				}
				if cfg.Storage.Backend != StorageBackendMinIO {
					t.Fatalf("expected storage backend %q, got %q", StorageBackendMinIO, cfg.Storage.Backend)
				}
				if cfg.Storage.MinIO.Endpoint != "minio.storage.svc.cluster.local:9000" {
					t.Fatalf("unexpected minio endpoint: %q", cfg.Storage.MinIO.Endpoint)
				}
				if cfg.Storage.MinIO.Bucket != "logs-data" {
					t.Fatalf("unexpected minio bucket: %q", cfg.Storage.MinIO.Bucket)
				}
				if cfg.Storage.MinIO.AccessKey != "minio-access" {
					t.Fatalf("unexpected minio access key: %q", cfg.Storage.MinIO.AccessKey)
				}
				if cfg.Storage.MinIO.SecretKey != "minio-secret" {
					t.Fatalf("unexpected minio secret key: %q", cfg.Storage.MinIO.SecretKey)
				}
				if !cfg.Storage.MinIO.UseSSL {
					t.Fatal("expected minio useSSL to be true")
				}
				if cfg.Storage.MinIO.LogsPrefix != "ingest" || cfg.Storage.MinIO.AnalyticsPrefix != "summary" {
					t.Fatalf(
						"unexpected minio prefixes: logs=%q analytics=%q",
						cfg.Storage.MinIO.LogsPrefix,
						cfg.Storage.MinIO.AnalyticsPrefix,
					)
				}
				if !cfg.Auth.Enabled {
					t.Fatal("expected auth.enabled to be true")
				}
				if cfg.Auth.HeaderName != "X-Lab-Key" {
					t.Fatalf("unexpected auth headerName: %q", cfg.Auth.HeaderName)
				}
				if len(cfg.Auth.Keys) != 1 || cfg.Auth.Keys[0] != "key-a" {
					t.Fatalf("unexpected auth keys: %v", cfg.Auth.Keys)
				}
			},
		},
		{
			name: "invalid storage backend returns error",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				path := filepath.Join(dir, "config.yaml")
				content := "storage:\n  backend: object-store\n"
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatalf("write config: %v", err)
				}
				return path
			},
			assertion: func(t *testing.T, _ *Config, err error) {
				if err == nil {
					t.Fatal("expected invalid storage backend error")
				}
				if !strings.Contains(err.Error(), "invalid storage backend") {
					t.Fatalf("unexpected error: %v", err)
				}
			},
		},
		{
			name: "invalid yaml returns error",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				path := filepath.Join(dir, "broken.yaml")
				if err := os.WriteFile(path, []byte(":\n"), 0o644); err != nil {
					t.Fatalf("write config: %v", err)
				}
				return path
			},
			assertion: func(t *testing.T, _ *Config, err error) {
				if err == nil {
					t.Fatal("expected invalid YAML error")
				}
			},
		},
		{
			name: "missing file returns error",
			setup: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing.yaml")
			},
			assertion: func(t *testing.T, _ *Config, err error) {
				if err == nil {
					t.Fatal("expected missing file error")
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := tc.setup(t)
			cfg, err := Load(path)
			tc.assertion(t, cfg, err)
		})
	}
}

func TestAggregationInterval(t *testing.T) {
	cases := []struct {
		name     string
		interval string
		fallback time.Duration
		want     time.Duration
		wantErr  bool
	}{
		{
			name:     "uses fallback when empty",
			interval: "",
			fallback: 30 * time.Second,
			want:     30 * time.Second,
		},
		{
			name:     "parses explicit duration",
			interval: "45s",
			fallback: 0,
			want:     45 * time.Second,
		},
		{
			name:     "invalid duration returns error",
			interval: "bad",
			fallback: 0,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Aggregation: AggregationConfig{Interval: tc.interval}}
			got, err := cfg.AggregationInterval(tc.fallback)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("AggregationInterval returned error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("unexpected duration: got=%v want=%v", got, tc.want)
			}
		})
	}
}

func TestParseDurationOrFallback(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		fallback time.Duration
		want     time.Duration
		wantErr  bool
	}{
		{
			name:     "empty uses fallback",
			value:    " ",
			fallback: 2 * time.Second,
			want:     2 * time.Second,
		},
		{
			name:    "invalid duration",
			value:   "not-a-duration",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDurationOrFallback(tc.value, tc.fallback)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected parse error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("unexpected duration: got=%v want=%v", got, tc.want)
			}
		})
	}
}
