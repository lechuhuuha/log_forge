package service

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/model"
)

type mockQueue struct {
	mu      sync.Mutex
	batches [][]model.LogRecord
	err     error
	notify  chan struct{}
	callCnt int
}

func (m *mockQueue) EnqueueBatch(ctx context.Context, records []model.LogRecord) error {
	m.mu.Lock()
	m.callCnt++
	m.mu.Unlock()
	if m.err == nil {
		m.mu.Lock()
		cp := make([]model.LogRecord, len(records))
		copy(cp, records)
		m.batches = append(m.batches, cp)
		m.mu.Unlock()
		if m.notify != nil {
			select {
			case m.notify <- struct{}{}:
			default:
			}
		}
	}
	return m.err
}

func (m *mockQueue) StartConsumers(ctx context.Context, handler func(context.Context, model.ConsumedMessage)) error {
	return nil
}

func TestProducerService(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "enqueue flushes batch to queue",
			run: func(t *testing.T) {
				records := []model.LogRecord{{
					Timestamp: time.Now(),
					Path:      "/home",
					UserAgent: "ua",
				}}
				q := &mockQueue{notify: make(chan struct{}, 1)}
				cfg := &ProducerConfig{QueueBufferSize: 4, Workers: 1, WriteTimeout: 5 * time.Millisecond}
				producer := NewProducerService(q, loggerpkg.NewNop(), cfg)
				t.Cleanup(producer.Close)
				producer.StartAsync()

				if err := producer.Enqueue(context.Background(), records); err != nil {
					t.Fatalf("Enqueue returned error: %v", err)
				}

				select {
				case <-q.notify:
				case <-time.After(time.Second):
					t.Fatalf("timed out waiting for producer to flush queue")
				}

				q.mu.Lock()
				defer q.mu.Unlock()
				if got := len(q.batches); got != 1 {
					t.Fatalf("expected 1 batch, got %d", got)
				}
			},
		},
		{
			name: "writes dlq on enqueue failures",
			run: func(t *testing.T) {
				dlqDir := filepath.Join(t.TempDir(), "dlq")
				q := &mockQueue{err: context.DeadlineExceeded}
				cfg := &ProducerConfig{
					QueueBufferSize:       1,
					Workers:               1,
					WriteTimeout:          5 * time.Millisecond,
					MaxRetries:            1,
					RetryBackoff:          1 * time.Millisecond,
					DLQDir:                dlqDir,
					QueueHighWaterPercent: 0.9,
				}
				producer := NewProducerService(q, loggerpkg.NewNop(), cfg)
				t.Cleanup(producer.Close)
				producer.StartAsync()

				records := []model.LogRecord{{
					Timestamp: time.Now(),
					Path:      "/fail",
					UserAgent: "ua",
				}}

				if err := producer.Enqueue(context.Background(), records); err != nil {
					t.Fatalf("Enqueue returned error: %v", err)
				}

				deadline := time.Now().Add(500 * time.Millisecond)
				for {
					q.mu.Lock()
					callCnt := q.callCnt
					q.mu.Unlock()
					if callCnt >= 2 { // initial try + 1 retry
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("expected retries to occur, callCnt=%d", callCnt)
					}
					time.Sleep(10 * time.Millisecond)
				}

				deadline = time.Now().Add(500 * time.Millisecond)
				found := false
				for !found && time.Now().Before(deadline) {
					matches, _ := filepath.Glob(filepath.Join(dlqDir, "*", "producer_*.json"))
					if len(matches) > 0 {
						found = true
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if !found {
					t.Fatalf("expected producer DLQ file to be written")
				}
			},
		},
		{
			name: "circuit breaker opens after repeated sync failures",
			run: func(t *testing.T) {
				q := &mockQueue{err: context.DeadlineExceeded}
				cfg := &ProducerConfig{
					QueueBufferSize:         1,
					Workers:                 1,
					WriteTimeout:            2 * time.Millisecond,
					MaxRetries:              0,
					CircuitFailureThreshold: 1,
					CircuitCooldown:         50 * time.Millisecond,
				}
				producer := NewProducerService(q, loggerpkg.NewNop(), cfg)
				t.Cleanup(producer.Close)
				producer.StartAsync()

				records := []model.LogRecord{{
					Timestamp: time.Now().UTC(),
					Path:      "/fail",
					UserAgent: "ua",
				}}

				if err := producer.EnqueueSync(context.Background(), records); err == nil {
					t.Fatal("expected initial sync enqueue to fail")
				}
				q.mu.Lock()
				callsAfterFirst := q.callCnt
				q.mu.Unlock()
				if callsAfterFirst == 0 {
					t.Fatal("expected queue to be called at least once")
				}

				err := producer.EnqueueSync(context.Background(), records)
				if !errors.Is(err, ErrProducerCircuitOpen) {
					t.Fatalf("expected ErrProducerCircuitOpen, got %v", err)
				}
				q.mu.Lock()
				callsAfterSecond := q.callCnt
				q.mu.Unlock()
				if callsAfterSecond != callsAfterFirst {
					t.Fatalf("expected no additional queue calls while circuit open: before=%d after=%d", callsAfterFirst, callsAfterSecond)
				}

				time.Sleep(60 * time.Millisecond)
				q.mu.Lock()
				q.err = nil
				q.mu.Unlock()

				if err := producer.EnqueueSync(context.Background(), records); err != nil {
					t.Fatalf("expected sync enqueue to recover after cooldown, got %v", err)
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
