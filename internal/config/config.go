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

// KafkaSettings captures Kafka queue configuration.
type KafkaSettings struct {
	Brokers        []string `yaml:"brokers"`
	Topic          string   `yaml:"topic"`
	GroupID        string   `yaml:"groupID"`
	BatchSize      int      `yaml:"batchSize"`
	BatchTimeout   string   `yaml:"batchTimeout"`
	Consumers      int      `yaml:"consumers"`
	RequireAllAcks bool     `yaml:"requireAllAcks"`
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
		c.Server.Addr = ":8080"
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
	c.Kafka.applyDefaults()
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
}

// AggregationInterval returns the configured interval or fallback.
func (c *Config) AggregationInterval(fallback time.Duration) (time.Duration, error) {
	return parseDurationOrFallback(c.Aggregation.Interval, fallback)
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
