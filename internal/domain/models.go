package domain

import (
	"context"
	"time"
)

// LogRecord represents a single log entry flowing through the system.
type LogRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Path      string    `json:"path"`
	UserAgent string    `json:"userAgent"`
}

// LogStore persists log records (Version 1 direct writes, Version 2 consumers).
type LogStore interface {
	SaveBatch(ctx context.Context, records []LogRecord) error
}

// LogQueue abstracts the queue used in Version 2 (Kafka implementation).
type LogQueue interface {
	EnqueueBatch(ctx context.Context, records []LogRecord) error
	StartConsumers(ctx context.Context, handler func(context.Context, ConsumedMessage)) error
}

// ConsumedMessage represents a log pulled from the queue along with commit metadata.
type ConsumedMessage struct {
	Record    LogRecord
	Partition int
	Offset    int64
	// Commit acknowledges the message after successful processing.
	Commit func(context.Context) error
}
