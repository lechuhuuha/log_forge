package queue

import (
	"context"

	"github.com/lechuhuuha/log_forge/internal/domain"
)

// NoopQueue is used in Version 1 where no queue is needed.
type NoopQueue struct{}

// EnqueueBatch is a no-op for Version 1.
func (NoopQueue) EnqueueBatch(ctx context.Context, records []domain.LogRecord) error {
	return nil
}

// StartConsumers is a no-op for Version 1.
func (NoopQueue) StartConsumers(ctx context.Context, handler func(context.Context, domain.LogRecord)) error {
	return nil
}
