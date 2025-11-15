package service

import (
	"context"
	"errors"
	"log"

	"github.com/example/logpipeline/internal/domain"
	"github.com/example/logpipeline/internal/metrics"
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
	logger *log.Logger
}

// NewIngestionService creates a new ingestion service.
func NewIngestionService(store domain.LogStore, queue domain.LogQueue, mode PipelineMode, logger *log.Logger) *IngestionService {
	if logger == nil {
		logger = log.Default()
	}
	return &IngestionService{
		store:  store,
		queue:  queue,
		mode:   mode,
		logger: logger,
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
			s.logger.Printf("failed to enqueue logs: %v", err)
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
			s.logger.Printf("failed to save logs: %v", err)
			return err
		}
		metrics.AddLogsIngested(len(records))
		return nil
	}
}
