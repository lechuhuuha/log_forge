package repo

import (
	"context"

	"github.com/lechuhuuha/log_forge/model"
)

// Repository persists log records.
type Repository interface {
	SaveBatch(ctx context.Context, records []model.LogRecord) error
}
