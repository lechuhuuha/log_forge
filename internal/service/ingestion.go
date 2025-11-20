package service

import (
	"context"
	"errors"

	"github.com/lechuhuuha/log_forge/internal/domain"
	"github.com/lechuhuuha/log_forge/internal/metrics"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
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

	workCh chan []domain.LogRecord
}

// NewIngestionService creates a new ingestion service.
func NewIngestionService(store domain.LogStore, queue domain.LogQueue, mode PipelineMode, logr loggerpkg.Logger) *IngestionService {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}

	if mode == ModeQueue {
		// large enough to absorb bursts but still bounded to avoid unbounded memory growth
		const workQueueSize = 65536
		const producerWorkers = 16

		workCh := make(chan []domain.LogRecord, workQueueSize)
		svc := &IngestionService{
			store:  store,
			queue:  queue,
			mode:   mode,
			logger: logr,
			workCh: workCh,
		}
		for i := 0; i < producerWorkers; i++ {
			go svc.runProducer()
		}
		return svc
	}
	return &IngestionService{
		store:  store,
		queue:  queue,
		mode:   mode,
		logger: logr,
	}
}

func (s *IngestionService) runProducer() {
	for batch := range s.workCh {
		if err := s.queue.EnqueueBatch(context.Background(), batch); err != nil {
			metrics.IncIngestErrors()
			s.logger.Error("failed to enqueue logs", loggerpkg.F("error", err))
		}
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
		select {
		case s.workCh <- records:
			return nil // returns quickly when buffer available; otherwise blocks until space or ctx cancellation
		case <-ctx.Done():
			return ctx.Err()
		}
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
