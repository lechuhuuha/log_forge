package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

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
	QueueBufferSize       int     `yaml:"queueBufferSize"`
	ProducerWorkers       int     `yaml:"producerWorkers"`
	ProducerWriteTimeout  string  `yaml:"producerWriteTimeout"`
	QueueHighWaterPercent float64 `yaml:"queueHighWaterPercent"`
}

// ConsumerSettings configures consumer-side batching to disk.
type ConsumerSettings struct {
	FlushSize      int    `yaml:"flushSize"`
	FlushInterval  string `yaml:"flushInterval"`
	PersistTimeout string `yaml:"persistTimeout"`
}

// KafkaSettings captures Kafka queue configuration.
type KafkaSettings struct {
	Brokers        []string `yaml:"brokers"`
	Topic          string   `yaml:"topic"`
	GroupID        string   `yaml:"groupID"`
	BatchSize      int      `yaml:"batchSize"`
	BatchTimeout   string   `yaml:"batchTimeout"`
	Consumers      int      `yaml:"consumers"`
	RequireAllAcks bool     `yaml:"requireAllAcks"`
	BatchBytes     int      `yaml:"batchBytes"`
	Compression    string   `yaml:"compression"`
	Async          bool     `yaml:"async"`
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
	if strings.TrimSpace(i.ProducerWriteTimeout) == "" {
		i.ProducerWriteTimeout = "10s"
	}
	if i.QueueHighWaterPercent == 0 {
		i.QueueHighWaterPercent = 0.9
	}
}

func (c *ConsumerSettings) applyDefaults() {
	if c.FlushSize == 0 {
		c.FlushSize = 512
	}
	if strings.TrimSpace(c.FlushInterval) == "" {
		c.FlushInterval = "500ms"
	}
	if strings.TrimSpace(c.PersistTimeout) == "" {
		c.PersistTimeout = "5s"
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
	if strings.TrimSpace(k.BatchTimeout) == "" {
		k.BatchTimeout = "1s"
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

// ProducerTimeout returns the configured producer write timeout.
func (i IngestionSettings) ProducerTimeout(fallback time.Duration) (time.Duration, error) {
	return parseDurationOrFallback(i.ProducerWriteTimeout, fallback)
}

// ConsumerFlushInterval returns the flush interval for consumer batching.
func (c ConsumerSettings) ConsumerFlushInterval(fallback time.Duration) (time.Duration, error) {
	return parseDurationOrFallback(c.FlushInterval, fallback)
}

// ConsumerPersistTimeout returns the persist timeout for consumer batch writes.
func (c ConsumerSettings) ConsumerPersistTimeout(fallback time.Duration) (time.Duration, error) {
	return parseDurationOrFallback(c.PersistTimeout, fallback)
}

// KafkaBatchTimeout returns the Kafka batch timeout duration.
func (k KafkaSettings) KafkaBatchTimeout(fallback time.Duration) (time.Duration, error) {
	return parseDurationOrFallback(k.BatchTimeout, fallback)
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
