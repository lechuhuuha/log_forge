package repo

import (
	"context"

	"github.com/lechuhuuha/log_forge/internal/domain"
)

// Repository persists log records.
type Repository interface {
	SaveBatch(ctx context.Context, records []domain.LogRecord) error
}
