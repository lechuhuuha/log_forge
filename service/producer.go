package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	EnqueueSync(ctx context.Context, batch []model.LogRecord) error
}

// ProducerConfig tunes buffering and retry behavior before writing to the queue.
type ProducerConfig struct {
	QueueBufferSize         int
	Workers                 int
	WriteTimeout            time.Duration
	QueueHighWaterPercent   float64
	MaxRetries              int
	RetryBackoff            time.Duration
	DLQDir                  string
	CircuitFailureThreshold int
	CircuitCooldown         time.Duration
}

const (
	defaultQueueBufferSize         = 10000
	defaultProducerWorkers         = 10
	defaultProducerWriteTimeout    = 10 * time.Second
	defaultQueueHighWaterPercent   = 0.9
	defaultProducerMaxRetries      = 3
	defaultProducerRetryBackoff    = 200 * time.Millisecond
	defaultProducerDLQDir          = "dlq/producer"
	defaultCircuitFailureThreshold = 5
	defaultCircuitCooldown         = 10 * time.Second
	producerTransientLogInterval   = 5 * time.Second
)

var (
	// ErrProducerStopped indicates the producer is no longer accepting work.
	ErrProducerStopped = errors.New("producer stopped")
	// ErrProducerNotStarted indicates Start has not been invoked.
	ErrProducerNotStarted = errors.New("producer not started")
	// ErrQueueFull indicates the producer buffer is at capacity.
	ErrQueueFull = errors.New("producer queue full")
	// ErrProducerCircuitOpen indicates the producer is temporarily rejecting work after repeated failures.
	ErrProducerCircuitOpen = errors.New("producer circuit open")
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

	workers                 int
	writeTimeout            time.Duration
	queueHighWaterPercent   float64
	maxRetries              int
	retryBackoff            time.Duration
	dlqDir                  string
	circuitFailureThreshold int
	circuitCooldown         time.Duration

	consecutiveFailures atomic.Int32
	circuitOpenUntilNs  atomic.Int64

	lastTransientErrorLogNs atomic.Int64
	transientErrorEvents    atomic.Uint64
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
	circuitFailureThreshold := defaultCircuitFailureThreshold
	circuitCooldown := defaultCircuitCooldown
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
		if cfg.CircuitFailureThreshold > 0 {
			circuitFailureThreshold = cfg.CircuitFailureThreshold
		}
		if cfg.CircuitCooldown > 0 {
			circuitCooldown = cfg.CircuitCooldown
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	svc := &ProducerService{
		queue:                   queue,
		logger:                  logr,
		workCh:                  make(chan []model.LogRecord, bufferSize),
		ctx:                     ctx,
		cancel:                  cancel,
		workers:                 workers,
		writeTimeout:            writeTimeout,
		queueHighWaterPercent:   highWater,
		maxRetries:              maxRetries,
		retryBackoff:            retryBackoff,
		dlqDir:                  dlqDir,
		circuitFailureThreshold: circuitFailureThreshold,
		circuitCooldown:         circuitCooldown,
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
// Can be call from other service to load data into channel
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
	if p.isCircuitOpen() {
		return ErrProducerCircuitOpen
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
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
	default:
		metrics.SetIngestionQueueDepth(len(p.workCh))
		return ErrQueueFull
	}
}

// EnqueueSync publishes a batch directly to the queue with retry logic.
// Can be call from other service to load data into queue.
func (p *ProducerService) EnqueueSync(ctx context.Context, batch []model.LogRecord) error {
	if len(batch) == 0 {
		return nil
	}
	if !p.started.Load() {
		return ErrProducerNotStarted
	}
	if p.closed.Load() {
		return ErrProducerStopped
	}
	if p.isCircuitOpen() {
		return ErrProducerCircuitOpen
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return p.tryEnqueueWithRetry(ctx, batch, -1)
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
		if err := p.tryEnqueueWithRetry(ctx, batch, workerID); err != nil {
			p.writeProducerDLQ(batch, err)
		}
		metrics.SetIngestionQueueDepth(len(p.workCh))
	}
}

func (p *ProducerService) tryEnqueueWithRetry(ctx context.Context, batch []model.LogRecord, workerID int) error {
	maxAttempts := p.maxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		produceCtx, cancel := context.WithTimeout(ctx, p.writeTimeout)
		err := p.queue.EnqueueBatch(produceCtx, batch)
		cancel()
		if err == nil {
			p.onEnqueueSuccess()
			return nil
		}
		lastErr = err
		metrics.IncIngestErrors()
		logFields := []loggerpkg.Field{
			loggerpkg.F("error", err),
			loggerpkg.F("attempt", attempt),
			loggerpkg.F("max_attempts", maxAttempts),
		}
		if workerID >= 0 {
			logFields = append(logFields, loggerpkg.F("worker_id", workerID))
		} else {
			logFields = append(logFields, loggerpkg.F("mode", "sync"))
		}
		p.logEnqueueFailure(err, logFields)
		if attempt == maxAttempts {
			p.onEnqueueFailure()
			return lastErr
		}
		backoff := p.retryBackoff * time.Duration(attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	if lastErr == nil {
		return errors.New("enqueue failed")
	}
	return lastErr
}

func (p *ProducerService) isCircuitOpen() bool {
	until := p.circuitOpenUntilNs.Load()
	if until == 0 {
		return false
	}
	now := time.Now().UTC().UnixNano()
	if now < until {
		return true
	}
	p.circuitOpenUntilNs.CompareAndSwap(until, 0)
	return false
}

func (p *ProducerService) onEnqueueSuccess() {
	p.consecutiveFailures.Store(0)
	p.circuitOpenUntilNs.Store(0)
}

func (p *ProducerService) onEnqueueFailure() {
	if p.circuitFailureThreshold <= 0 || p.circuitCooldown <= 0 {
		return
	}
	failures := p.consecutiveFailures.Add(1)
	if int(failures) < p.circuitFailureThreshold {
		return
	}
	until := time.Now().UTC().Add(p.circuitCooldown)
	p.circuitOpenUntilNs.Store(until.UnixNano())
	p.consecutiveFailures.Store(0)
	p.logger.Warn("producer circuit opened",
		loggerpkg.F("cooldown", p.circuitCooldown.String()),
		loggerpkg.F("open_until", until.Format(time.RFC3339Nano)))
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

func (p *ProducerService) logEnqueueFailure(err error, fields []loggerpkg.Field) {
	if !isTransientProducerError(err) {
		p.logger.Error("failed to enqueue logs", fields...)
		return
	}

	_ = p.transientErrorEvents.Add(1)

	now := time.Now().UTC().UnixNano()
	last := p.lastTransientErrorLogNs.Load()
	if now-last < int64(producerTransientLogInterval) {
		return
	}
	if !p.lastTransientErrorLogNs.CompareAndSwap(last, now) {
		return
	}

	events := p.transientErrorEvents.Swap(0)
	if events == 0 {
		events = 1
	}
	logFields := make([]loggerpkg.Field, 0, len(fields)+1)
	logFields = append(logFields, fields...)
	logFields = append(logFields, loggerpkg.F("events", events))
	p.logger.Warn("failed to enqueue logs (transient, suppressed)", logFields...)
}

func isTransientProducerError(err error) bool {
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled),
		errors.Is(err, ErrQueueFull),
		errors.Is(err, ErrProducerCircuitOpen):
		return true
	}

	var netErr net.Error
	if errors.As(err, &netErr) && (netErr.Timeout()) {
		return true
	}

	msg := strings.ToLower(err.Error())
	knownTransientSubstrings := []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"i/o timeout",
		"dial tcp",
		"network is unreachable",
		"leader not available",
		"temporary failure",
	}
	for _, sub := range knownTransientSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}
