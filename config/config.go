package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CLIConfig captures CLI-provided options for starting the server.
type CLIConfig struct {
	Addr                string
	Version             int
	LogsDir             string
	AnalyticsDir        string
	AggregationInterval time.Duration
	ConfigPath          string
}

// ParseFlags reads CLI parameters and returns a Config with defaults applied.
func ParseFlags() CLIConfig {
	addr := flag.String("addr", "", "HTTP listen address (optional override)")
	versionFlag := flag.Int("version", 0, "Pipeline version (optional override)")
	logsDir := flag.String("logs-dir", "", "Directory for raw logs (optional override)")
	analyticsDir := flag.String("analytics-dir", "", "Directory for analytics output (optional override)")
	aggInterval := flag.Duration("aggregation-interval", 0, "Aggregation interval (optional override)")
	configPath := flag.String("config", "", "Path to YAML config file (required)")

	flag.Parse()

	return CLIConfig{
		Addr:                *addr,
		Version:             *versionFlag,
		LogsDir:             *logsDir,
		AnalyticsDir:        *analyticsDir,
		AggregationInterval: *aggInterval,
		ConfigPath:          strings.TrimSpace(*configPath),
	}
}

// Config models the YAML configuration file.
type Config struct {
	Version     int               `yaml:"version"`
	Server      ServerConfig      `yaml:"server"`
	Storage     StorageConfig     `yaml:"storage"`
	Aggregation AggregationConfig `yaml:"aggregation"`
	Ingestion   IngestionSettings `yaml:"ingestion"`
	Consumer    ConsumerSettings  `yaml:"consumer"`
	Kafka       KafkaSettings     `yaml:"kafka"`
}

// ServerConfig contains HTTP server settings.
type ServerConfig struct {
	Addr string `yaml:"addr"`
}

// StorageConfig configures directories for raw logs and analytics output.
type StorageConfig struct {
	LogsDir      string `yaml:"logsDir"`
	AnalyticsDir string `yaml:"analyticsDir"`
}

// AggregationConfig defines the periodic aggregation interval.
type AggregationConfig struct {
	Interval string `yaml:"interval"`
}

// IngestionSettings tunes producer-side buffering before Kafka.
type IngestionSettings struct {
	QueueBufferSize       int           `yaml:"queueBufferSize"`
	ProducerWorkers       int           `yaml:"producerWorkers"`
	ProducerWriteTimeout  time.Duration `yaml:"producerWriteTimeout"`
	QueueHighWaterPercent float64       `yaml:"queueHighWaterPercent"`
}

// ConsumerSettings configures consumer-side batching to disk.
type ConsumerSettings struct {
	FlushSize      int           `yaml:"flushSize"`
	FlushInterval  time.Duration `yaml:"flushInterval"`
	PersistTimeout time.Duration `yaml:"persistTimeout"`
}

// KafkaSettings captures Kafka queue configuration.
type KafkaSettings struct {
	Brokers        []string      `yaml:"brokers"`
	Topic          string        `yaml:"topic"`
	GroupID        string        `yaml:"groupID"`
	BatchSize      int           `yaml:"batchSize"`
	BatchTimeout   time.Duration `yaml:"batchTimeout"`
	Consumers      int           `yaml:"consumers"`
	RequireAllAcks bool          `yaml:"requireAllAcks"`
	BatchBytes     int           `yaml:"batchBytes"`
	Compression    string        `yaml:"compression"`
	Async          bool          `yaml:"async"`
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
	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.Version == 0 {
		c.Version = 1
	}
	if strings.TrimSpace(c.Server.Addr) == "" {
		c.Server.Addr = ":8082"
	}
	if strings.TrimSpace(c.Storage.LogsDir) == "" {
		c.Storage.LogsDir = "logs"
	}
	if strings.TrimSpace(c.Storage.AnalyticsDir) == "" {
		c.Storage.AnalyticsDir = "analytics"
	}
	if strings.TrimSpace(c.Aggregation.Interval) == "" {
		c.Aggregation.Interval = "1m"
	}
	c.Ingestion.applyDefaults()
	c.Consumer.applyDefaults()
	c.Kafka.applyDefaults()
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
