package bootstrap

import (
	"context"
	"sync"
	"time"

	"github.com/lechuhuuha/log_forge/internal/domain"
	"github.com/lechuhuuha/log_forge/internal/metrics"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

const (
	defaultConsumerFlushSize      = 512
	defaultConsumerFlushInterval  = 500 * time.Millisecond
	defaultConsumerPersistTimeout = 5 * time.Second
)

// ConsumerBatchConfig allows tuning consumer-side batching.
type ConsumerBatchConfig struct {
	FlushSize      int
	FlushInterval  time.Duration
	PersistTimeout time.Duration
}

// consumerBatchWriter coalesces records from Kafka consumers before hitting disk.
type consumerBatchWriter struct {
	store          domain.LogStore
	logger         loggerpkg.Logger
	flushSize      int
	flushInterval  time.Duration
	persistTimeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	pending []domain.LogRecord
	done    chan struct{}
}

func newConsumerBatchWriter(ctx context.Context, store domain.LogStore, cfg ConsumerBatchConfig, logr loggerpkg.Logger) *consumerBatchWriter {
	if ctx == nil {
		ctx = context.Background()
	}
	if cfg.FlushSize <= 0 {
		cfg.FlushSize = defaultConsumerFlushSize
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = defaultConsumerFlushInterval
	}
	if cfg.PersistTimeout <= 0 {
		cfg.PersistTimeout = defaultConsumerPersistTimeout
	}
	if logr == nil {
		logr = loggerpkg.NewNop()
	}

	batchCtx, cancel := context.WithCancel(ctx)
	writer := &consumerBatchWriter{
		store:          store,
		logger:         logr,
		flushSize:      cfg.FlushSize,
		flushInterval:  cfg.FlushInterval,
		persistTimeout: cfg.PersistTimeout,
		ctx:            batchCtx,
		cancel:         cancel,
		pending:        make([]domain.LogRecord, 0, cfg.FlushSize),
		done:           make(chan struct{}),
	}
	go writer.flushLoop()
	return writer
}

func (w *consumerBatchWriter) Add(rec domain.LogRecord) {
	if w == nil {
		return
	}
	select {
	case <-w.ctx.Done():
		return
	default:
	}

	w.mu.Lock()
	w.pending = append(w.pending, rec)
	if len(w.pending) >= w.flushSize {
		batch := w.drainLocked()
		w.mu.Unlock()
		w.persist(batch, false)
		return
	}
	w.mu.Unlock()
}

func (w *consumerBatchWriter) Close() {
	if w == nil {
		return
	}
	w.cancel()
	<-w.done
}

func (w *consumerBatchWriter) flushLoop() {
	ticker := time.NewTicker(w.flushInterval)
	defer func() {
		ticker.Stop()
		// final flush even though context is canceled
		w.flush(true)
		close(w.done)
	}()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.flush(false)
		}
	}
}

func (w *consumerBatchWriter) flush(force bool) {
	batch := w.takePending()
	if len(batch) == 0 {
		return
	}
	w.persist(batch, force)
}

func (w *consumerBatchWriter) takePending() []domain.LogRecord {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.drainLocked()
}

func (w *consumerBatchWriter) drainLocked() []domain.LogRecord {
	if len(w.pending) == 0 {
		return nil
	}
	batch := make([]domain.LogRecord, len(w.pending))
	copy(batch, w.pending)
	w.pending = w.pending[:0]
	return batch
}

func (w *consumerBatchWriter) persist(batch []domain.LogRecord, force bool) {
	if len(batch) == 0 {
		return
	}
	ctx := w.ctx
	if force {
		ctx = context.Background()
	}
	writeCtx, cancel := context.WithTimeout(ctx, w.persistTimeout)
	defer cancel()
	if err := w.store.SaveBatch(writeCtx, batch); err != nil {
		metrics.IncIngestErrors()
		w.logger.Error("consumer batch write failed",
			loggerpkg.F("error", err),
			loggerpkg.F("batch_size", len(batch)))
		return
	}
	metrics.AddLogsIngested(len(batch))
}
