package service

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

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
	// ErrProducerNotConfigured indicates queue mode is enabled without a producer.
	ErrProducerNotConfigured = errors.New("producer not configured")
	// ErrLogStoreNotConfigured indicates direct mode is enabled without a repository.
	ErrLogStoreNotConfigured = errors.New("log store not configured")
)

const ingestionTransientLogInterval = 5 * time.Second

// IngestionService orchestrates log ingestion across versions.
type IngestionService struct {
	repo         repo.Repository
	producer     Producer
	mode         PipelineMode
	logger       loggerpkg.Logger
	syncOnIngest bool

	closed atomic.Bool

	lastTransientQueueLogNs atomic.Int64
	transientQueueErrEvents atomic.Uint64
}

// NewIngestionService creates a new ingestion service.
func NewIngestionService(repository repo.Repository, producer Producer, mode PipelineMode, syncOnIngest bool, logr loggerpkg.Logger) *IngestionService {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	return &IngestionService{
		repo:         repository,
		producer:     producer,
		mode:         mode,
		logger:       logr,
		syncOnIngest: syncOnIngest,
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
			return ErrProducerNotConfigured
		}
		if s.syncOnIngest {
			if err := s.producer.EnqueueSync(ctx, records); err != nil {
				metrics.IncIngestErrors()
				s.logQueueEnqueueError(err)
				return err
			}
		} else if err := s.producer.Enqueue(ctx, records); err != nil {
			metrics.IncIngestErrors()
			s.logQueueEnqueueError(err)
			return err
		}
		metrics.IncBatchesReceived()
		return nil
	case ModeDirect:
		fallthrough
	default:
		if s.repo == nil {
			return ErrLogStoreNotConfigured
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

func (s *IngestionService) logQueueEnqueueError(err error) {
	if !isTransientQueueError(err) {
		s.logger.Error("failed to enqueue logs", loggerpkg.F("error", err))
		return
	}

	_ = s.transientQueueErrEvents.Add(1)

	now := time.Now().UTC().UnixNano()
	last := s.lastTransientQueueLogNs.Load()
	if now-last < int64(ingestionTransientLogInterval) {
		return
	}
	if !s.lastTransientQueueLogNs.CompareAndSwap(last, now) {
		return
	}

	events := s.transientQueueErrEvents.Swap(0)
	if events == 0 {
		events = 1
	}
	s.logger.Warn(
		"failed to enqueue logs (transient, suppressed)",
		loggerpkg.F("error", err),
		loggerpkg.F("events", events),
	)
}

func isTransientQueueError(err error) bool {
	switch {
	case errors.Is(err, ErrProducerCircuitOpen),
		errors.Is(err, ErrQueueFull),
		errors.Is(err, ErrProducerNotStarted),
		errors.Is(err, ErrProducerStopped),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled):
		return true
	default:
		return false
	}
}
