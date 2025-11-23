package queue

import (
	"context"

	"github.com/lechuhuuha/log_forge/model"
)

// NoopQueue is used in Version 1 where no queue is needed.
type NoopQueue struct{}

// EnqueueBatch is a no-op for Version 1.
func (NoopQueue) EnqueueBatch(ctx context.Context, records []model.LogRecord) error {
	return nil
}

// StartConsumers is a no-op for Version 1.
func (NoopQueue) StartConsumers(ctx context.Context, handler func(context.Context, model.ConsumedMessage)) error {
	return nil
}
