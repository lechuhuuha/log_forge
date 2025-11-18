package service

import (
	"context"
	"errors"

	"github.com/example/logpipeline/internal/domain"
	"github.com/example/logpipeline/internal/metrics"
	loggerpkg "github.com/example/logpipeline/logger"
)

// PipelineMode determines how ingestion behaves.
type PipelineMode int

const (
	// ModeDirect writes directly to storage (Version 1).
	ModeDirect PipelineMode = iota
	// ModeQueue enqueues to Kafka (Version 2).
	ModeQueue
)

// IngestionService orchestrates log ingestion across versions.
type IngestionService struct {
	store  domain.LogStore
	queue  domain.LogQueue
	mode   PipelineMode
	logger loggerpkg.Logger
}

// NewIngestionService creates a new ingestion service.
func NewIngestionService(store domain.LogStore, queue domain.LogQueue, mode PipelineMode, logr loggerpkg.Logger) *IngestionService {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	return &IngestionService{
		store:  store,
		queue:  queue,
		mode:   mode,
		logger: logr,
	}
}

// Mode returns the configured pipeline mode.
func (s *IngestionService) Mode() PipelineMode {
	return s.mode
}

// ProcessBatch routes the logs to storage or queue depending on the mode.
func (s *IngestionService) ProcessBatch(ctx context.Context, records []domain.LogRecord) error {
	if len(records) == 0 {
		return nil
	}

	switch s.mode {
	case ModeQueue:
		if s.queue == nil {
			return errors.New("log queue not configured")
		}
		if err := s.queue.EnqueueBatch(ctx, records); err != nil {
			metrics.IncIngestErrors()
			s.logger.Error("failed to enqueue logs", loggerpkg.F("error", err))
			return err
		}
		return nil
	case ModeDirect:
		fallthrough
	default:
		if s.store == nil {
			return errors.New("log store not configured")
		}
		if err := s.store.SaveBatch(ctx, records); err != nil {
			metrics.IncIngestErrors()
			s.logger.Error("failed to save logs", loggerpkg.F("error", err))
			return err
		}
		metrics.AddLogsIngested(len(records))
		return nil
	}
}
