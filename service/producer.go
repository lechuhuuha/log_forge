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

	"github.com/lechuhuuha/log_forge/internal/metrics"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/model"
)

// Producer sends log batches to a downstream queue.
type Producer interface {
	Enqueue(ctx context.Context, batch []model.LogRecord) error
}

// ProducerConfig tunes buffering and retry behavior before writing to the queue.
type ProducerConfig struct {
	QueueBufferSize       int
	Workers               int
	WriteTimeout          time.Duration
	QueueHighWaterPercent float64
	MaxRetries            int
	RetryBackoff          time.Duration
	DLQDir                string
}

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
	// ErrProducerStopped indicates the producer is no longer accepting work.
	ErrProducerStopped = errors.New("producer stopped")
	// ErrProducerNotStarted indicates Start has not been invoked.
	ErrProducerNotStarted = errors.New("producer not started")
)

// ProducerService implements buffered, retried publishing to a LogQueue.
type ProducerService struct {
	queue  model.LogQueue
	logger loggerpkg.Logger

	workCh    chan []model.LogRecord
	wg        sync.WaitGroup
	startOnce sync.Once
	closeOnce sync.Once
	closed    atomic.Bool
	started   atomic.Bool

	ctx    context.Context
	cancel context.CancelFunc

	queueBufferSize       int
	workers               int
	writeTimeout          time.Duration
	queueHighWaterPercent float64
	maxRetries            int
	retryBackoff          time.Duration
	dlqDir                string
}

// NewProducerService wires producer workers around the provided queue.
func NewProducerService(queue model.LogQueue, logr loggerpkg.Logger, cfg *ProducerConfig) *ProducerService {
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
		if cfg.Workers > 0 {
			workers = cfg.Workers
		}
		if cfg.WriteTimeout > 0 {
			writeTimeout = cfg.WriteTimeout
		}
		if cfg.QueueHighWaterPercent > 0 {
			highWater = cfg.QueueHighWaterPercent
		}
		if cfg.MaxRetries >= 0 {
			maxRetries = cfg.MaxRetries
		}
		if cfg.RetryBackoff > 0 {
			retryBackoff = cfg.RetryBackoff
		}
		if cfg.DLQDir != "" {
			dlqDir = cfg.DLQDir
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	svc := &ProducerService{
		queue:                 queue,
		logger:                logr,
		workCh:                make(chan []model.LogRecord, bufferSize),
		ctx:                   ctx,
		cancel:                cancel,
		queueBufferSize:       bufferSize,
		workers:               workers,
		writeTimeout:          writeTimeout,
		queueHighWaterPercent: highWater,
		maxRetries:            maxRetries,
		retryBackoff:          retryBackoff,
		dlqDir:                dlqDir,
	}

	return svc
}

// Start launches producer workers. Safe to call multiple times.
func (p *ProducerService) Start() {
	p.startOnce.Do(func() {
		for i := 0; i < p.workers; i++ {
			p.wg.Add(1)
			go p.runProducer(i)
		}
		p.started.Store(true)
	})
}

// Enqueue buffers a batch for asynchronous publishing.
func (p *ProducerService) Enqueue(ctx context.Context, batch []model.LogRecord) error {
	if len(batch) == 0 {
		return nil
	}
	if !p.started.Load() {
		return ErrProducerNotStarted
	}
	if p.closed.Load() {
		return ErrProducerStopped
	}
	select {
	case p.workCh <- batch:
		currentDepth := len(p.workCh)
		metrics.SetIngestionQueueDepth(currentDepth)
		if currentDepth >= int(float64(cap(p.workCh))*p.queueHighWaterPercent) {
			p.logger.Warn("ingestion work queue nearing capacity",
				loggerpkg.F("depth", currentDepth),
				loggerpkg.F("capacity", cap(p.workCh)))
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops accepting new work and waits for in-flight batches to finish.
func (p *ProducerService) Close() {
	p.closeOnce.Do(func() {
		p.closed.Store(true)
		if p.workCh != nil {
			close(p.workCh)
			metrics.SetIngestionQueueDepth(0)
		}
		if p.cancel != nil {
			p.cancel()
		}
		p.wg.Wait()
	})
}

func (p *ProducerService) runProducer(workerID int) {
	defer p.wg.Done()
	for batch := range p.workCh {
		metrics.SetIngestionQueueDepth(len(p.workCh))
		ctx := p.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		success := p.tryEnqueueWithRetry(ctx, batch, workerID)
		if !success {
			p.writeProducerDLQ(batch, fmt.Errorf("enqueue failed after %d retries", p.maxRetries))
		}
		metrics.SetIngestionQueueDepth(len(p.workCh))
	}
}

func (p *ProducerService) tryEnqueueWithRetry(ctx context.Context, batch []model.LogRecord, workerID int) bool {
	maxAttempts := p.maxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		produceCtx, cancel := context.WithTimeout(ctx, p.writeTimeout)
		err := p.queue.EnqueueBatch(produceCtx, batch)
		cancel()
		if err == nil {
			return true
		}
		metrics.IncIngestErrors()
		p.logger.Error("failed to enqueue logs",
			loggerpkg.F("error", err),
			loggerpkg.F("worker_id", workerID),
			loggerpkg.F("attempt", attempt),
			loggerpkg.F("max_attempts", maxAttempts))
		if attempt == maxAttempts {
			return false
		}
		backoff := p.retryBackoff * time.Duration(attempt)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(backoff):
		}
	}
	return false
}

func (p *ProducerService) writeProducerDLQ(batch []model.LogRecord, reason error) {
	if p.dlqDir == "" || len(batch) == 0 {
		return
	}
	dateDir := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(p.dlqDir, dateDir, fmt.Sprintf("producer_%d.json", time.Now().UTC().UnixNano()))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		p.logger.Warn("failed to create producer dlq directory", loggerpkg.F("error", err))
		return
	}
	entry := struct {
		Records []model.LogRecord `json:"records"`
		Reason  string            `json:"reason"`
		Time    time.Time         `json:"time"`
	}{
		Records: batch,
		Reason:  reason.Error(),
		Time:    time.Now().UTC(),
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		p.logger.Warn("failed to marshal producer dlq batch", loggerpkg.F("error", err))
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		p.logger.Warn("failed to write producer dlq batch", loggerpkg.F("error", err), loggerpkg.F("path", path))
		return
	}
	p.logger.Warn("wrote producer batch to dlq", loggerpkg.F("path", path), loggerpkg.F("count", len(batch)))
}
