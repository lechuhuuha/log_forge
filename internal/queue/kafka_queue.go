package queue

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	kafka "github.com/segmentio/kafka-go"

	"github.com/lechuhuuha/log_forge/internal/domain"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

// KafkaLogQueue implements LogQueue backed by Kafka.
type KafkaLogQueue struct {
	writer      *kafka.Writer
	readerCfg   kafka.ReaderConfig
	consumers   int
	logger      loggerpkg.Logger
	closeWriter sync.Once
}

// KafkaConfig holds configuration for Kafka queue.
type KafkaConfig struct {
	Brokers        []string
	Topic          string
	GroupID        string
	BatchSize      int
	BatchTimeout   time.Duration
	Consumers      int
	RequireAllAcks bool
}

// NewKafkaLogQueue builds a Kafka-backed queue implementation.
func NewKafkaLogQueue(cfg KafkaConfig, logr loggerpkg.Logger) (*KafkaLogQueue, error) {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	if len(cfg.Brokers) == 0 {
		return nil, errors.New("kafka brokers must be provided")
	}
	if cfg.Topic == "" {
		cfg.Topic = "logs"
	}
	if cfg.GroupID == "" {
		cfg.GroupID = "logs-consumer-group"
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.BatchTimeout == 0 {
		cfg.BatchTimeout = time.Second
	}
	if cfg.Consumers <= 0 {
		cfg.Consumers = 1
	}

	requiredAcks := kafka.RequireOne
	if cfg.RequireAllAcks {
		requiredAcks = kafka.RequireAll
	}

	writer := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Topic:        cfg.Topic,
		Async:        true,
		BatchSize:    cfg.BatchSize,
		BatchTimeout: cfg.BatchTimeout,
		RequiredAcks: requiredAcks,
		Balancer:     &kafka.Hash{},
	}

	readerCfg := kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		Topic:    cfg.Topic,
		GroupID:  cfg.GroupID,
		MinBytes: 1,
		MaxBytes: 10e6,
	}

	return &KafkaLogQueue{
		writer:    writer,
		readerCfg: readerCfg,
		consumers: cfg.Consumers,
		logger:    logr,
	}, nil
}

// EnqueueBatch encodes and produces the records to Kafka.
func (q *KafkaLogQueue) EnqueueBatch(ctx context.Context, records []domain.LogRecord) error {
	if len(records) == 0 {
		return nil
	}
	messages := make([]kafka.Message, len(records))
	for i, rec := range records {
		data, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		key := []byte(rec.Path)
		messages[i] = kafka.Message{
			Key:   key,
			Value: data,
			Time:  rec.Timestamp,
		}
	}
	return q.writer.WriteMessages(ctx, messages...)
}

// StartConsumers spawns background consumer goroutines.
func (q *KafkaLogQueue) StartConsumers(ctx context.Context, handler func(context.Context, domain.LogRecord)) error {
	if handler == nil {
		return errors.New("handler required")
	}
	if q.consumers <= 0 {
		q.consumers = 1
	}
	for i := 0; i < q.consumers; i++ {
		reader := kafka.NewReader(q.readerCfg)
		go q.consume(ctx, reader, handler)
	}
	return nil
}

func (q *KafkaLogQueue) consume(ctx context.Context, reader *kafka.Reader, handler func(context.Context, domain.LogRecord)) {
	defer reader.Close()
	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return
			}
			q.logger.Error("kafka consumer error", loggerpkg.F("error", err))
			// brief pause before retrying to avoid tight loop
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var rec domain.LogRecord
		if err := json.Unmarshal(msg.Value, &rec); err != nil {
			q.logger.Warn("discard malformed kafka message", loggerpkg.F("error", err))
			continue
		}
		handler(ctx, rec)
	}
}

// Close flushes the Kafka writer.
func (q *KafkaLogQueue) Close() error {
	if q == nil || q.writer == nil {
		return nil
	}
	var err error
	q.closeWriter.Do(func() {
		err = q.writer.Close()
	})
	return err
}
