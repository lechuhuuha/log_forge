package service

import (
	"context"
	"sync"

	"github.com/lechuhuuha/log_forge/internal/domain"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

// PipelineService owns consumer-side lifecycle for queue mode.
type PipelineService struct {
	queue     domain.LogQueue
	store     domain.LogStore
	cfg       ConsumerBatchConfig
	logger    loggerpkg.Logger
	closer    func() error
	writer    *consumerBatchWriter
	cancel    context.CancelFunc
	startMu   sync.Mutex
	started   bool
	startErr  error
	closeOnce sync.Once
}

// NewPipelineService builds a pipeline with the queue consumer and batch writer wiring.
func NewPipelineService(queue domain.LogQueue, store domain.LogStore, cfg ConsumerBatchConfig, logr loggerpkg.Logger, closer func() error) *PipelineService {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	return &PipelineService{
		queue:  queue,
		store:  store,
		cfg:    cfg,
		logger: logr,
		closer: closer,
	}
}

// Start launches consumers and batch writer. It is safe to call once.
func (p *PipelineService) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	p.startMu.Lock()
	defer p.startMu.Unlock()
	if p.started {
		return p.startErr
	}
	p.started = true

	consumeCtx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.writer = newConsumerBatchWriter(consumeCtx, p.store, p.cfg, p.logger)

	if p.queue == nil {
		p.startErr = ErrIngestionStopped // reuse an existing sentinel for "not running"
		p.logger.Error("pipeline start failed: queue not configured")
		cancel()
		p.writer.Close()
		return p.startErr
	}

	if err := p.queue.StartConsumers(consumeCtx, func(c context.Context, msg domain.ConsumedMessage) {
		p.writer.Add(msg)
	}); err != nil {
		p.startErr = err
		cancel()
		p.writer.Close()
		p.writer = nil
		return err
	}
	return nil
}

// Close stops consumers and the batch writer; safe to call multiple times.
func (p *PipelineService) Close() {
	p.closeOnce.Do(func() {
		if p.cancel != nil {
			p.cancel()
		}
		if p.writer != nil {
			p.writer.Close()
		}
		if p.closer != nil {
			_ = p.closer()
		}
	})
}
