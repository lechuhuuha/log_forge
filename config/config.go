package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultAuthHeaderName = "X-API-Key"

// CLIConfig captures CLI-provided options for starting the server.
type CLIConfig struct {
	// ConfigPath points to the YAML configuration file used at startup.
	ConfigPath string
}

// ParseFlags reads CLI parameters for config-file-based startup.
func ParseFlags() CLIConfig {
	configPath := flag.String("config", "", "Path to YAML config file (required)")

	flag.Parse()

	return CLIConfig{
		ConfigPath: strings.TrimSpace(*configPath),
	}
}

// Config models the YAML configuration file.
type Config struct {
	// Version selects pipeline behavior (1=direct write, 2=Kafka-backed).
	Version int `yaml:"version"`
	// Server groups HTTP server and timeout settings.
	Server ServerConfig `yaml:"server"`
	// Storage configures on-disk destinations for raw logs and analytics summaries.
	Storage StorageConfig `yaml:"storage"`
	// Auth controls optional API key authentication for ingestion endpoints.
	Auth AuthSettings `yaml:"auth"`
	// Aggregation controls periodic analytics generation.
	Aggregation AggregationConfig `yaml:"aggregation"`
	// Ingestion controls producer-side buffering/retry/circuit behavior.
	Ingestion IngestionSettings `yaml:"ingestion"`
	// Consumer controls consumer-side flush/persist behavior.
	Consumer ConsumerSettings `yaml:"consumer"`
	// Kafka configures broker/topic and Kafka client behavior.
	Kafka KafkaSettings `yaml:"kafka"`
}

// ServerConfig contains HTTP server settings.
type ServerConfig struct {
	// Addr is the HTTP bind address (for example, ":8082").
	Addr string `yaml:"addr"`
	// ReadHeaderTimeout limits time to read request headers.
	ReadHeaderTimeout time.Duration `yaml:"readHeaderTimeout"`
	// ReadTimeout limits total time to read a request.
	ReadTimeout time.Duration `yaml:"readTimeout"`
	// WriteTimeout limits total time to write a response.
	WriteTimeout time.Duration `yaml:"writeTimeout"`
	// IdleTimeout limits keep-alive idle time.
	IdleTimeout time.Duration `yaml:"idleTimeout"`
	// RequestTimeout is the application-level timeout used for /logs processing.
	RequestTimeout time.Duration `yaml:"requestTimeout"`
}

// AuthSettings controls API key authentication behavior.
type AuthSettings struct {
	// Enabled toggles API key authentication.
	Enabled bool `yaml:"enabled"`
	// HeaderName is the request header used to pass API keys.
	HeaderName string `yaml:"headerName"`
	// Keys contains accepted API keys.
	Keys []string `yaml:"keys"`
}

// StorageBackend identifies storage implementation.
type StorageBackend string

const (
	// StorageBackendFile stores records on local filesystem.
	StorageBackendFile StorageBackend = "file"
	// StorageBackendMinIO stores records in MinIO-compatible object storage.
	StorageBackendMinIO StorageBackend = "minio"
)

// StorageConfig configures storage backend, filesystem directories, and MinIO settings.
type StorageConfig struct {
	// Backend selects where logs are persisted.
	Backend StorageBackend `yaml:"backend"`
	// LogsDir is the base directory for NDJSON log files.
	LogsDir string `yaml:"logsDir"`
	// AnalyticsDir is the base directory for aggregated summary files.
	AnalyticsDir string `yaml:"analyticsDir"`
	// MinIO contains object-storage settings used when backend=minio.
	MinIO MinIOSettings `yaml:"minio"`
}

// MinIOSettings contains object-storage configuration for the MinIO backend.
type MinIOSettings struct {
	// Endpoint is the host:port address of MinIO.
	Endpoint string `yaml:"endpoint"`
	// Bucket is the target bucket for persisted records.
	Bucket string `yaml:"bucket"`
	// AccessKey is the MinIO access key ID.
	AccessKey string `yaml:"accessKey"`
	// SecretKey is the MinIO secret key.
	SecretKey string `yaml:"secretKey"`
	// UseSSL enables TLS for MinIO client requests.
	UseSSL bool `yaml:"useSSL"`
	// LogsPrefix is the object key prefix for raw log files.
	LogsPrefix string `yaml:"logsPrefix"`
	// AnalyticsPrefix is the object key prefix for aggregated analytics files.
	AnalyticsPrefix string `yaml:"analyticsPrefix"`
}

// AggregationConfig defines the periodic aggregation interval.
type AggregationConfig struct {
	// Interval defines how often aggregation runs (for example, "30s").
	Interval string `yaml:"interval"`
}

// IngestionSettings tunes producer-side buffering before Kafka.
type IngestionSettings struct {
	// QueueBufferSize is the number of batches buffered before producers.
	QueueBufferSize int `yaml:"queueBufferSize"`
	// ProducerWorkers is the number of producer goroutines.
	ProducerWorkers int `yaml:"producerWorkers"`
	// ProducerWriteTimeout limits a single Kafka write attempt.
	ProducerWriteTimeout time.Duration `yaml:"producerWriteTimeout"`
	// QueueHighWaterPercent triggers warning logs near queue saturation.
	QueueHighWaterPercent float64 `yaml:"queueHighWaterPercent"`
	// SyncOnIngest forces /logs requests to wait for Kafka write outcome.
	SyncOnIngest bool `yaml:"syncOnIngest"`
	// CircuitFailureThreshold is consecutive failures needed to open the circuit.
	CircuitFailureThreshold int `yaml:"circuitFailureThreshold"`
	// CircuitCooldown is how long the circuit remains open before retrying.
	CircuitCooldown time.Duration `yaml:"circuitCooldown"`
}

// ConsumerSettings configures consumer-side batching to disk.
type ConsumerSettings struct {
	// FlushSize is the max number of consumed messages per persist batch.
	FlushSize int `yaml:"flushSize"`
	// FlushInterval is the max time to wait before forcing a flush.
	FlushInterval time.Duration `yaml:"flushInterval"`
	// PersistTimeout limits the write-to-storage operation per flush.
	PersistTimeout time.Duration `yaml:"persistTimeout"`
}

// KafkaSettings captures Kafka queue configuration.
type KafkaSettings struct {
	// Brokers is the list of server instance in an Apache Kafka cluster.
	Brokers []string `yaml:"brokers"`
	// Topic is the topic used for log ingestion.
	Topic string `yaml:"topic"`
	// GroupID is the consumer group used by v2 workers.
	GroupID string `yaml:"groupID"`
	// BatchSize is the max number of messages per producer batch.
	BatchSize int `yaml:"batchSize"`
	// BatchTimeout is the max wait before flushing a producer batch.
	BatchTimeout time.Duration `yaml:"batchTimeout"`
	// Consumers is the number of consumer goroutines/readers to start.
	Consumers int `yaml:"consumers"`
	// RequireAllAcks controls producer acknowledgement strictness.
	RequireAllAcks bool `yaml:"requireAllAcks"`
	// BatchBytes caps producer batch size in bytes.
	BatchBytes int `yaml:"batchBytes"`
	// Compression selects Kafka message compression codec.
	Compression string `yaml:"compression"`
	// Async enables kafka-go async writer mode.
	Async bool `yaml:"async"`
}

// Load parses a YAML configuration file from disk.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = 1
	}
	if strings.TrimSpace(c.Server.Addr) == "" {
		c.Server.Addr = ":8082"
	}
	c.Server.applyDefaults()
	c.Storage.applyDefaults()
	c.Auth.applyDefaults()
	if strings.TrimSpace(c.Aggregation.Interval) == "" {
		c.Aggregation.Interval = "1m"
	}
	c.Ingestion.applyDefaults()
	c.Consumer.applyDefaults()
	c.Kafka.applyDefaults()
}

func (c *Config) validate() error {
	if !c.Storage.Backend.IsValid() {
		return fmt.Errorf("invalid storage backend %q", c.Storage.Backend)
	}
	return nil
}

func (a *AuthSettings) applyDefaults() {
	a.HeaderName = strings.TrimSpace(a.HeaderName)
	if a.HeaderName == "" {
		a.HeaderName = defaultAuthHeaderName
	}
	if len(a.Keys) == 0 {
		return
	}
	keys := make([]string, 0, len(a.Keys))
	for _, key := range a.Keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	a.Keys = keys
}

func (b StorageBackend) normalized() StorageBackend {
	return StorageBackend(strings.ToLower(strings.TrimSpace(string(b))))
}

// IsValid reports whether the storage backend is supported.
func (b StorageBackend) IsValid() bool {
	switch b.normalized() {
	case StorageBackendFile, StorageBackendMinIO:
		return true
	default:
		return false
	}
}

func (s *StorageConfig) applyDefaults() {
	s.Backend = s.Backend.normalized()
	if s.Backend == "" {
		s.Backend = StorageBackendFile
	}
	if strings.TrimSpace(s.LogsDir) == "" {
		s.LogsDir = "logs"
	}
	if strings.TrimSpace(s.AnalyticsDir) == "" {
		s.AnalyticsDir = "analytics"
	}
	if strings.TrimSpace(s.MinIO.LogsPrefix) == "" {
		s.MinIO.LogsPrefix = "logs"
	}
	if strings.TrimSpace(s.MinIO.AnalyticsPrefix) == "" {
		s.MinIO.AnalyticsPrefix = "analytics"
	}
}

func (s *ServerConfig) applyDefaults() {
	if s.ReadHeaderTimeout == 0 {
		s.ReadHeaderTimeout = 2 * time.Second
	}
	if s.ReadTimeout == 0 {
		s.ReadTimeout = 10 * time.Second
	}
	if s.WriteTimeout == 0 {
		s.WriteTimeout = 10 * time.Second
	}
	if s.IdleTimeout == 0 {
		s.IdleTimeout = 60 * time.Second
	}
	if s.RequestTimeout == 0 {
		s.RequestTimeout = 5 * time.Second
	}
}

func (i *IngestionSettings) applyDefaults() {
	if i.QueueBufferSize == 0 {
		i.QueueBufferSize = 10000
	}
	if i.ProducerWorkers == 0 {
		i.ProducerWorkers = 10
	}
	if i.ProducerWriteTimeout == 0 {
		i.ProducerWriteTimeout = 10 * time.Second
	}
	if i.QueueHighWaterPercent == 0 {
		i.QueueHighWaterPercent = 0.9
	}
	if i.CircuitFailureThreshold <= 0 {
		i.CircuitFailureThreshold = 5
	}
	if i.CircuitCooldown == 0 {
		i.CircuitCooldown = 10 * time.Second
	}
}

func (c *ConsumerSettings) applyDefaults() {
	if c.FlushSize == 0 {
		c.FlushSize = 512
	}
	if c.FlushInterval == 0 {
		c.FlushInterval = 500 * time.Millisecond
	}
	if c.PersistTimeout == 0 {
		c.PersistTimeout = 5 * time.Second
	}
}

func (k *KafkaSettings) applyDefaults() {
	if strings.TrimSpace(k.Topic) == "" {
		k.Topic = "logs"
	}
	if strings.TrimSpace(k.GroupID) == "" {
		k.GroupID = "logs-consumer-group"
	}
	if k.BatchSize == 0 {
		k.BatchSize = 100
	}
	if k.BatchTimeout == 0 {
		k.BatchTimeout = time.Second
	}
	if k.Consumers == 0 {
		k.Consumers = 1
	}
	if strings.TrimSpace(k.Compression) == "" {
		k.Compression = "none"
	}
}

// AggregationInterval returns the configured interval or fallback.
func (c *Config) AggregationInterval(fallback time.Duration) (time.Duration, error) {
	return parseDurationOrFallback(c.Aggregation.Interval, fallback)
}

func parseDurationOrFallback(value string, fallback time.Duration) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	return d, nil
}
