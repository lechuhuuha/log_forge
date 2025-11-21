package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

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

const (
	defaultQueueBufferSize       = 10000
	defaultProducerWorkers       = 10
	defaultProducerWriteTimeout  = 10 * time.Second
	defaultQueueHighWaterPercent = 0.9
)

var (
	// ErrIngestionStopped indicates the ingestion service is no longer accepting work.
	ErrIngestionStopped = errors.New("ingestion service stopped")
)

// IngestionConfig allows tuning of producer-side buffering and workers.
type IngestionConfig struct {
	QueueBufferSize       int
	ProducerWorkers       int
	ProducerWriteTimeout  time.Duration
	QueueHighWaterPercent float64
}

// IngestionService orchestrates log ingestion across versions.
type IngestionService struct {
	store  domain.LogStore
	queue  domain.LogQueue
	mode   PipelineMode
	logger loggerpkg.Logger

	workCh    chan []domain.LogRecord
	wg        sync.WaitGroup
	closeOnce sync.Once
	closed    atomic.Bool

	producerCtx    context.Context
	producerCancel context.CancelFunc

	queueBufferSize       int
	producerWorkers       int
	producerWriteTimeout  time.Duration
	queueHighWaterPercent float64
}

// NewIngestionService creates a new ingestion service.
func NewIngestionService(store domain.LogStore, queue domain.LogQueue, mode PipelineMode, logr loggerpkg.Logger, cfg *IngestionConfig) *IngestionService {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}

	bufferSize := defaultQueueBufferSize
	workers := defaultProducerWorkers
	writeTimeout := defaultProducerWriteTimeout
	highWater := defaultQueueHighWaterPercent
	if cfg != nil {
		if cfg.QueueBufferSize > 0 {
			bufferSize = cfg.QueueBufferSize
		}
		if cfg.ProducerWorkers > 0 {
			workers = cfg.ProducerWorkers
		}
		if cfg.ProducerWriteTimeout > 0 {
			writeTimeout = cfg.ProducerWriteTimeout
		}
		if cfg.QueueHighWaterPercent > 0 {
			highWater = cfg.QueueHighWaterPercent
		}
	}

	if mode == ModeQueue {
		producerCtx, cancel := context.WithCancel(context.Background())
		workCh := make(chan []domain.LogRecord, bufferSize)
		svc := &IngestionService{
			store:                 store,
			queue:                 queue,
			mode:                  mode,
			logger:                logr,
			workCh:                workCh,
			producerCtx:           producerCtx,
			producerCancel:        cancel,
			queueBufferSize:       bufferSize,
			producerWorkers:       workers,
			producerWriteTimeout:  writeTimeout,
			queueHighWaterPercent: highWater,
		}
		for i := 0; i < workers; i++ {
			svc.wg.Add(1)
			go svc.runProducer(i)
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

func (s *IngestionService) runProducer(workerID int) {
	defer s.wg.Done()
	for batch := range s.workCh {
		metrics.SetIngestionQueueDepth(len(s.workCh))
		ctx := s.producerCtx
		if ctx == nil {
			ctx = context.Background()
		}
		produceCtx, cancel := context.WithTimeout(ctx, s.producerWriteTimeout)
		if err := s.queue.EnqueueBatch(produceCtx, batch); err != nil {
			metrics.IncIngestErrors()
			s.logger.Error("failed to enqueue logs", loggerpkg.F("error", err), loggerpkg.F("worker_id", workerID))
			cancel()
			continue
		}
		cancel()
		metrics.SetIngestionQueueDepth(len(s.workCh))
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
		if s.closed.Load() {
			return ErrIngestionStopped
		}
		select {
		case s.workCh <- records:
			currentDepth := len(s.workCh)
			metrics.SetIngestionQueueDepth(currentDepth)
			if currentDepth >= int(float64(cap(s.workCh))*s.queueHighWaterPercent) {
				s.logger.Warn("ingestion work queue nearing capacity",
					loggerpkg.F("depth", currentDepth),
					loggerpkg.F("capacity", cap(s.workCh)))
			}
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

// Close releases internal resources and stops producer workers.
func (s *IngestionService) Close() {
	if s.mode != ModeQueue {
		return
	}
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		if s.workCh != nil {
			close(s.workCh)
			metrics.SetIngestionQueueDepth(0)
		}
		if s.producerCancel != nil {
			s.producerCancel()
		}
		s.wg.Wait()
	})
}
