package service

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/lechuhuuha/log_forge/internal/metrics"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/model"
	"github.com/lechuhuuha/log_forge/repo"
)

// PipelineMode determines how ingestion behaves.
type PipelineMode int

const (
	// ModeDirect writes directly to storage (Version 1).
	ModeDirect PipelineMode = iota
	// ModeQueue enqueues to Kafka (Version 2).
	ModeQueue
)

var (
	// ErrIngestionStopped indicates the ingestion service is no longer accepting work.
	ErrIngestionStopped = errors.New("ingestion service stopped")
)

// IngestionService orchestrates log ingestion across versions.
type IngestionService struct {
	repo     repo.Repository
	producer Producer
	mode     PipelineMode
	logger   loggerpkg.Logger

	closed atomic.Bool
}

// NewIngestionService creates a new ingestion service.
func NewIngestionService(repository repo.Repository, producer Producer, mode PipelineMode, logr loggerpkg.Logger) *IngestionService {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	return &IngestionService{
		repo:     repository,
		producer: producer,
		mode:     mode,
		logger:   logr,
	}
}

// Mode returns the configured pipeline mode.
func (s *IngestionService) Mode() PipelineMode {
	return s.mode
}

// ProcessBatch routes the logs to storage or queue depending on the mode.
func (s *IngestionService) ProcessBatch(ctx context.Context, records []model.LogRecord) error {
	if len(records) == 0 {
		return nil
	}

	switch s.mode {
	case ModeQueue:
		if s.closed.Load() {
			return ErrIngestionStopped
		}
		if s.producer == nil {
			return errors.New("producer not configured")
		}
		if err := s.producer.Enqueue(ctx, records); err != nil {
			metrics.IncIngestErrors()
			s.logger.Error("failed to enqueue logs", loggerpkg.F("error", err))
			return err
		}
		metrics.IncBatchesReceived()
		return nil
	case ModeDirect:
		fallthrough
	default:
		if s.repo == nil {
			return errors.New("log store not configured")
		}
		if err := s.repo.SaveBatch(ctx, records); err != nil {
			metrics.IncIngestErrors()
			s.logger.Error("failed to save logs", loggerpkg.F("error", err))
			return err
		}
		metrics.IncBatchesReceived()
		metrics.AddLogsIngested(len(records))
		return nil
	}
}

// Close marks ingestion as stopped.
func (s *IngestionService) Close() {
	if s.mode == ModeQueue {
		s.closed.Store(true)
	}
}
