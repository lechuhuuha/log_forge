package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kafka "github.com/segmentio/kafka-go"
	kafkagzip "github.com/segmentio/kafka-go/gzip"
	kafkalz4 "github.com/segmentio/kafka-go/lz4"
	kafkasnappy "github.com/segmentio/kafka-go/snappy"
	kafkazstd "github.com/segmentio/kafka-go/zstd"

	"github.com/lechuhuuha/log_forge/config"
	"github.com/lechuhuuha/log_forge/internal/metrics"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

type kafkaWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

type kafkaReader interface {
	FetchMessage(ctx context.Context) (kafka.Message, error)
	CommitMessages(ctx context.Context, msgs ...kafka.Message) error
	Close() error
}

// KafkaLogQueue implements LogQueue backed by Kafka.
type KafkaLogQueue struct {
	writer        kafkaWriter
	readerFactory func(kafka.ReaderConfig) kafkaReader
	readerCfg     kafka.ReaderConfig
	consumers     int
	logger        loggerpkg.Logger
	// activeConsumers tracks how many goroutines are processing a message.
	activeConsumers int32
	closeWriter     sync.Once
	kafkaDown       atomic.Bool
	lastDownLogNs   atomic.Int64
	lastWriterLogNs atomic.Int64
}

const (
	kafkaDownLogInterval   = 5 * time.Second
	kafkaWriterLogInterval = 30 * time.Second
	kafkaRetryMinBackoff   = 250 * time.Millisecond
	kafkaRetryMaxBackoff   = 5 * time.Second
)

// NewKafkaLogQueue builds a Kafka-backed queue implementation.
func NewKafkaLogQueue(cfg config.KafkaSettings, logr loggerpkg.Logger) (*KafkaLogQueue, error) {
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

	readerCfg := kafka.ReaderConfig{
		Brokers:  cfg.Brokers,
		Topic:    cfg.Topic,
		GroupID:  cfg.GroupID,
		MinBytes: 1,
		MaxBytes: 10e6,
	}

	queue := &KafkaLogQueue{
		readerCfg:     readerCfg,
		readerFactory: defaultKafkaReaderFactory,
		consumers:     cfg.Consumers,
		logger:        logr,
	}

	queue.writer = kafka.NewWriter(kafka.WriterConfig{
		Brokers: cfg.Brokers,
		Topic:   cfg.Topic,
		// Balancer:     &kafka.Hash{},
		BatchSize:    cfg.BatchSize,
		BatchBytes:   cfg.BatchBytes,
		Async:        cfg.Async,
		BatchTimeout: cfg.BatchTimeout,
		RequiredAcks: int(requiredAcks),
		ErrorLogger: kafka.LoggerFunc(func(msg string, args ...interface{}) {
			queue.logWriterError(fmt.Sprintf(msg, args...))
		}),
		CompressionCodec: compressionCodec(cfg.Compression),
	})

	return queue, nil
}

func defaultKafkaReaderFactory(cfg kafka.ReaderConfig) kafkaReader {
	return kafka.NewReader(cfg)
}

func compressionCodec(name string) kafka.CompressionCodec {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "none":
		return nil
	case "gzip":
		return kafkagzip.NewCompressionCodec()
	case "snappy":
		return kafkasnappy.NewCompressionCodec()
	case "lz4":
		return kafkalz4.NewCompressionCodec()
	case "zstd", "zstandard":
		return kafkazstd.NewCompressionCodec()
	default:
		return nil
	}
}

// CheckConnectivity verifies at least one broker is reachable.
func (q *KafkaLogQueue) CheckConnectivity(ctx context.Context) error {
	if q == nil {
		return errors.New("kafka queue not configured")
	}
	if len(q.readerCfg.Brokers) == 0 {
		return errors.New("kafka brokers not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	dialer := &kafka.Dialer{}
	for _, broker := range q.readerCfg.Brokers {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := dialer.DialContext(ctx, "tcp", broker)
		if err != nil {
			lastErr = err
			continue
		}
		_ = conn.Close()
		metrics.SetKafkaUp(true)
		q.kafkaDown.Store(false)
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no brokers to dial")
	}
	return fmt.Errorf("dial kafka brokers: %w", lastErr)
}

func (q *KafkaLogQueue) markKafkaDown(err error) {
	metrics.IncKafkaConsumerErrors()
	metrics.SetKafkaUp(false)

	wasDown := q.kafkaDown.Swap(true)
	now := time.Now().UTC().UnixNano()
	if !wasDown {
		q.lastDownLogNs.Store(now)
		q.logger.Warn("kafka became unavailable", loggerpkg.F("error", err))
		return
	}

	last := q.lastDownLogNs.Load()
	if now-last < int64(kafkaDownLogInterval) {
		return
	}
	if q.lastDownLogNs.CompareAndSwap(last, now) {
		q.logger.Warn("kafka still unavailable", loggerpkg.F("error", err))
	}
}

func (q *KafkaLogQueue) markKafkaUp(partition int, offset int64) {
	metrics.SetKafkaUp(true)
	if q.kafkaDown.Swap(false) {
		q.logger.Info("kafka connectivity recovered", loggerpkg.F("partition", partition), loggerpkg.F("offset", offset))
	}
}

func (q *KafkaLogQueue) logWriterError(msg string) {
	metrics.IncKafkaWriterErrors()
	metrics.SetKafkaUp(false)

	now := time.Now().UTC().UnixNano()
	last := q.lastWriterLogNs.Load()
	if now-last < int64(kafkaWriterLogInterval) {
		return
	}
	if q.lastWriterLogNs.CompareAndSwap(last, now) {
		q.logger.Warn("kafka writer internal error (suppressed)", loggerpkg.F("sample", msg))
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
