package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	defaultProducerMaxRetries    = 3
	defaultProducerRetryBackoff  = 200 * time.Millisecond
	defaultProducerDLQDir        = "dlq/producer"
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
	ProducerMaxRetries    int
	ProducerRetryBackoff  time.Duration
	ProducerDLQDir        string
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
	producerMaxRetries    int
	producerRetryBackoff  time.Duration
	producerDLQDir        string
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
	maxRetries := defaultProducerMaxRetries
	retryBackoff := defaultProducerRetryBackoff
	dlqDir := defaultProducerDLQDir
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
		if cfg.ProducerMaxRetries >= 0 {
			maxRetries = cfg.ProducerMaxRetries
		}
		if cfg.ProducerRetryBackoff > 0 {
			retryBackoff = cfg.ProducerRetryBackoff
		}
		if cfg.ProducerDLQDir != "" {
			dlqDir = cfg.ProducerDLQDir
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
			producerMaxRetries:    maxRetries,
			producerRetryBackoff:  retryBackoff,
			producerDLQDir:        dlqDir,
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
		success := s.tryEnqueueWithRetry(ctx, batch, workerID)
		if !success {
			s.writeProducerDLQ(batch, fmt.Errorf("enqueue failed after %d retries", s.producerMaxRetries))
		}
		metrics.SetIngestionQueueDepth(len(s.workCh))
	}
}

func (s *IngestionService) tryEnqueueWithRetry(ctx context.Context, batch []domain.LogRecord, workerID int) bool {
	maxAttempts := s.producerMaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		produceCtx, cancel := context.WithTimeout(ctx, s.producerWriteTimeout)
		err := s.queue.EnqueueBatch(produceCtx, batch)
		cancel()
		if err == nil {
			return true
		}
		metrics.IncIngestErrors()
		s.logger.Error("failed to enqueue logs",
			loggerpkg.F("error", err),
			loggerpkg.F("worker_id", workerID),
			loggerpkg.F("attempt", attempt),
			loggerpkg.F("max_attempts", maxAttempts))
		if attempt == maxAttempts {
			return false
		}
		backoff := s.producerRetryBackoff * time.Duration(attempt)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
	}
	return false
}

func (s *IngestionService) writeProducerDLQ(batch []domain.LogRecord, reason error) {
	if s.producerDLQDir == "" || len(batch) == 0 {
		return
	}
	dateDir := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(s.producerDLQDir, dateDir, fmt.Sprintf("producer_%d.json", time.Now().UTC().UnixNano()))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.logger.Warn("failed to create producer dlq directory", loggerpkg.F("error", err))
		return
	}
	entry := struct {
		Records []domain.LogRecord `json:"records"`
		Reason  string             `json:"reason"`
		Time    time.Time          `json:"time"`
	}{
		Records: batch,
		Reason:  reason.Error(),
		Time:    time.Now().UTC(),
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		s.logger.Warn("failed to marshal producer dlq batch", loggerpkg.F("error", err))
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		s.logger.Warn("failed to write producer dlq batch", loggerpkg.F("error", err), loggerpkg.F("path", path))
		return
	}
	s.logger.Warn("wrote producer batch to dlq", loggerpkg.F("path", path), loggerpkg.F("count", len(batch)))
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
			metrics.IncBatchesReceived()
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
		metrics.IncBatchesReceived()
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
