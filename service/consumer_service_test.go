package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/model"
)

type consumerRepoStub struct {
	mu      sync.Mutex
	batches [][]model.LogRecord
	err     error
}

func (r *consumerRepoStub) SaveBatch(ctx context.Context, records []model.LogRecord) error {
	if r.err != nil {
		return r.err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]model.LogRecord, len(records))
	copy(cp, records)
	r.batches = append(r.batches, cp)
	return nil
}

type consumerQueueStub struct {
	startErr error
	emits    []model.ConsumedMessage
}

func (q *consumerQueueStub) EnqueueBatch(ctx context.Context, records []model.LogRecord) error {
	return nil
}

func (q *consumerQueueStub) StartConsumers(ctx context.Context, handler func(context.Context, model.ConsumedMessage)) error {
	if q.startErr != nil {
		return q.startErr
	}
	for _, msg := range q.emits {
		handler(ctx, msg)
	}
	return nil
}

func TestConsumerService(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "start with nil queue returns error",
			run: func(t *testing.T) {
				svc := NewConsumerService(nil, &consumerRepoStub{}, ConsumerBatchConfig{}, nil, nil)
				if err := svc.Start(context.Background()); err == nil {
					t.Fatal("expected error when queue is nil")
				}
			},
		},
		{
			name: "start propagates queue start error",
			run: func(t *testing.T) {
				svc := NewConsumerService(
					&consumerQueueStub{startErr: errors.New("start failed")},
					&consumerRepoStub{},
					ConsumerBatchConfig{},
					nil,
					nil,
				)
				if err := svc.Start(context.Background()); err == nil {
					t.Fatal("expected consumer start error")
				}
				svc.Close()
			},
		},
		{
			name: "start persists and commits",
			run: func(t *testing.T) {
				var commitCalls atomic.Int32
				repo := &consumerRepoStub{}
				msg := model.ConsumedMessage{
					Record: model.LogRecord{
						Timestamp: time.Now().UTC(),
						Path:      "/ok",
						UserAgent: "ua",
					},
					Commit: func(context.Context) error {
						commitCalls.Add(1)
						return errors.New("commit failed") // warning branch
					},
				}
				svc := NewConsumerService(
					&consumerQueueStub{emits: []model.ConsumedMessage{msg}},
					repo,
					ConsumerBatchConfig{FlushSize: 1, FlushInterval: time.Hour, PersistTimeout: time.Second},
					nil,
					nil,
				)

				if err := svc.Start(context.Background()); err != nil {
					t.Fatalf("Start returned error: %v", err)
				}
				svc.Close()

				repo.mu.Lock()
				defer repo.mu.Unlock()
				if len(repo.batches) != 1 || len(repo.batches[0]) != 1 {
					t.Fatalf("expected one persisted record, got batches=%d", len(repo.batches))
				}
				if commitCalls.Load() != 1 {
					t.Fatalf("expected commit to be called once, got %d", commitCalls.Load())
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t)
		})
	}
}

func TestConsumerWriter(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "defaults and noop paths",
			run: func(t *testing.T) {
				repo := &consumerRepoStub{}
				writer := newConsumerWriter(context.TODO(), repo, ConsumerBatchConfig{}, nil)
				if writer.flushSize != defaultConsumerFlushSize {
					t.Fatalf("expected default flush size %d, got %d", defaultConsumerFlushSize, writer.flushSize)
				}
				if writer.flushInterval != defaultConsumerFlushInterval {
					t.Fatalf("expected default flush interval %v, got %v", defaultConsumerFlushInterval, writer.flushInterval)
				}
				if writer.persistTimeout != defaultConsumerPersistTimeout {
					t.Fatalf("expected default persist timeout %v, got %v", defaultConsumerPersistTimeout, writer.persistTimeout)
				}
				if writer.dlqDir != "dlq" {
					t.Fatalf("expected default DLQ dir dlq, got %q", writer.dlqDir)
				}

				writer.persist(nil, false)
				writer.flush(false)
				writer.cancel()
				writer.Add(model.ConsumedMessage{}) // no-op once canceled
				writer.Close()
			},
		},
		{
			name: "writes dlq on persist failure",
			run: func(t *testing.T) {
				dlqDir := filepath.Join(t.TempDir(), "dlq")
				repo := &consumerRepoStub{err: errors.New("disk write failed")}
				writer := newConsumerWriter(
					context.Background(),
					repo,
					ConsumerBatchConfig{
						FlushSize:      100,
						FlushInterval:  time.Hour,
						PersistTimeout: 50 * time.Millisecond,
						DLQDir:         dlqDir,
					},
					nil,
				)

				var commitCalls atomic.Int32
				writer.Add(model.ConsumedMessage{
					Record: model.LogRecord{
						Timestamp: time.Now().UTC(),
						Path:      "/fail",
						UserAgent: "ua",
					},
					Commit: func(context.Context) error {
						commitCalls.Add(1)
						return nil
					},
				})
				writer.Close() // triggers forced flush path

				matches, err := filepath.Glob(filepath.Join(dlqDir, "*", "consumer_*.json"))
				if err != nil {
					t.Fatalf("glob dlq: %v", err)
				}
				if len(matches) == 0 {
					t.Fatal("expected consumer DLQ file to be written")
				}
				if commitCalls.Load() != 0 {
					t.Fatalf("expected no commit calls on persist failure, got %d", commitCalls.Load())
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			tc.run(t)
		})
	}
}
