package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	DLQDir         string
}

// consumerBatchWriter coalesces records from Kafka consumers before hitting disk.
type consumerBatchWriter struct {
	store          domain.LogStore
	logger         loggerpkg.Logger
	flushSize      int
	flushInterval  time.Duration
	persistTimeout time.Duration
	dlqDir         string

	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	pending []domain.ConsumedMessage
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
	if cfg.DLQDir == "" {
		cfg.DLQDir = "dlq"
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
		dlqDir:         cfg.DLQDir,
		ctx:            batchCtx,
		cancel:         cancel,
		pending:        make([]domain.ConsumedMessage, 0, cfg.FlushSize),
		done:           make(chan struct{}),
	}
	go writer.flushLoop()
	return writer
}

// Add appends a consumed message and triggers flush when the batch is full.
func (w *consumerBatchWriter) Add(msg domain.ConsumedMessage) {
	if w == nil {
		return
	}
	select {
	case <-w.ctx.Done():
		return
	default:
	}

	w.mu.Lock()
	w.pending = append(w.pending, msg)
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

func (w *consumerBatchWriter) takePending() []domain.ConsumedMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.drainLocked()
}

func (w *consumerBatchWriter) drainLocked() []domain.ConsumedMessage {
	if len(w.pending) == 0 {
		return nil
	}
	batch := make([]domain.ConsumedMessage, len(w.pending))
	copy(batch, w.pending)
	w.pending = w.pending[:0]
	return batch
}

func (w *consumerBatchWriter) persist(batch []domain.ConsumedMessage, force bool) {
	if len(batch) == 0 {
		return
	}
	ctx := w.ctx
	if force {
		ctx = context.Background()
	}
	writeCtx, cancel := context.WithTimeout(ctx, w.persistTimeout)
	defer cancel()
	records := make([]domain.LogRecord, len(batch))
	for i := range batch {
		records[i] = batch[i].Record
	}
	if err := w.store.SaveBatch(writeCtx, records); err != nil {
		metrics.IncIngestErrors()
		w.logger.Error("consumer batch write failed",
			loggerpkg.F("error", err),
			loggerpkg.F("batch_size", len(batch)))
		w.writeDLQ(batch, err)
		return
	}
	metrics.AddLogsIngested(len(batch))
	for _, msg := range batch {
		if msg.Commit != nil {
			if err := msg.Commit(ctx); err != nil {
				w.logger.Warn("failed to commit kafka message after persist",
					loggerpkg.F("error", err),
					loggerpkg.F("partition", msg.Partition),
					loggerpkg.F("offset", msg.Offset))
			}
		}
	}
}

func (w *consumerBatchWriter) writeDLQ(batch []domain.ConsumedMessage, reason error) {
	if w.dlqDir == "" {
		return
	}
	dateDir := time.Now().UTC().Format("2006-01-02")
	path := filepath.Join(w.dlqDir, dateDir, fmt.Sprintf("consumer_%d.json", time.Now().UTC().UnixNano()))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		w.logger.Warn("failed to create dlq directory", loggerpkg.F("error", err))
		return
	}

	type dlqEntry struct {
		Record    domain.LogRecord `json:"record"`
		Partition int              `json:"partition"`
		Offset    int64            `json:"offset"`
		Reason    string           `json:"reason"`
	}
	entries := make([]dlqEntry, len(batch))
	for i := range batch {
		entries[i] = dlqEntry{
			Record:    batch[i].Record,
			Partition: batch[i].Partition,
			Offset:    batch[i].Offset,
			Reason:    reason.Error(),
		}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		w.logger.Warn("failed to marshal dlq batch", loggerpkg.F("error", err))
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		w.logger.Warn("failed to write dlq batch", loggerpkg.F("error", err), loggerpkg.F("path", path))
		return
	}
	w.logger.Warn("wrote batch to dlq", loggerpkg.F("path", path), loggerpkg.F("count", len(batch)))
}
