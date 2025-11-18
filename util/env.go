package util

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/example/logpipeline/internal/queue"
	loggerpkg "github.com/example/logpipeline/logger"
)

func GetEnv(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func GetIntEnv(key string, fallback int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return fallback
	}
	return parsed
}

func GetDurationEnv(key string, fallback time.Duration) time.Duration {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return fallback
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		return fallback
	}
	return d
}

func CreateKafkaQueueFromEnv(logger loggerpkg.Logger) (*queue.KafkaLogQueue, error) {
	if logger == nil {
		logger = loggerpkg.NewNop()
	}
	brokersEnv := strings.TrimSpace(os.Getenv(KafkaBrokers))
	if brokersEnv == "" {
		return nil, errors.New("KAFKA_BROKERS must be set for version 2")
	}
	brokerParts := strings.Split(brokersEnv, ",")
	brokers := make([]string, 0, len(brokerParts))
	for _, b := range brokerParts {
		b = strings.TrimSpace(b)
		if b != "" {
			brokers = append(brokers, b)
		}
	}
	if len(brokers) == 0 {
		return nil, errors.New("no valid Kafka brokers provided")
	}

	cfg := queue.KafkaConfig{
		Brokers:        brokers,
		Topic:          GetEnv(KafkaTopic, DefaultKafkaTopic),
		GroupID:        GetEnv(KafkaGroupID, DefaultKafkaGroupID),
		BatchSize:      GetIntEnv(KafkaBatchSize, DefaultKafkaBatchSize),
		BatchTimeout:   GetDurationEnv(KafkaBatchTimeout, DefaultKafkaBatchTimeout),
		Consumers:      GetIntEnv(KafkaConsumers, DefaultKafkaConsumers),
		RequireAllAcks: strings.EqualFold(os.Getenv(KafkaAcks), "all"),
	}

	return queue.NewKafkaLogQueue(cfg, logger)
}
