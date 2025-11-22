package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/internal/domain"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

type mockStore struct {
	batches [][]domain.LogRecord
	err     error
}

func (m *mockStore) SaveBatch(ctx context.Context, records []domain.LogRecord) error {
	if m.err == nil {
		cp := make([]domain.LogRecord, len(records))
		copy(cp, records)
		m.batches = append(m.batches, cp)
	}
	return m.err
}

type mockQueue struct {
	mu      sync.Mutex
	batches [][]domain.LogRecord
	err     error
	notify  chan struct{}
}

func (m *mockQueue) EnqueueBatch(ctx context.Context, records []domain.LogRecord) error {
	if m.err == nil {
		m.mu.Lock()
		cp := make([]domain.LogRecord, len(records))
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

func (m *mockQueue) StartConsumers(ctx context.Context, handler func(context.Context, domain.LogRecord)) error {
	return nil
}

func TestIngestionService_ProcessBatch(t *testing.T) {
	now := time.Now()
	records := []domain.LogRecord{{
		Timestamp: now,
		Path:      "/home",
		UserAgent: "ua",
	}}

	cases := []struct {
		name          string
		mode          PipelineMode
		store         *mockStore
		queue         *mockQueue
		cfg           *IngestionConfig
		expectErr     bool
		expectBatches int
	}{
		{
			name:          "direct mode uses store",
			mode:          ModeDirect,
			store:         &mockStore{},
			expectBatches: 1,
		},
		{
			name:          "queue mode uses queue",
			mode:          ModeQueue,
			queue:         &mockQueue{notify: make(chan struct{}, 1)},
			cfg:           &IngestionConfig{QueueBufferSize: 4, ProducerWorkers: 1},
			expectBatches: 1,
		},
		{
			name:      "direct mode propagates errors",
			mode:      ModeDirect,
			store:     &mockStore{err: context.DeadlineExceeded},
			expectErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc := NewIngestionService(tc.store, tc.queue, tc.mode, loggerpkg.NewNop(), tc.cfg)
			if tc.mode == ModeQueue {
				t.Cleanup(svc.Close)
			}

			err := svc.ProcessBatch(context.Background(), records)
			if tc.expectErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
			} else if err != nil {
				t.Fatalf("ProcessBatch returned error: %v", err)
			}

			switch tc.mode {
			case ModeDirect:
				if tc.store != nil && len(tc.store.batches) != tc.expectBatches {
					t.Fatalf("expected %d batches in store, got %d", tc.expectBatches, len(tc.store.batches))
				}
			case ModeQueue:
				if tc.queue != nil {
					select {
					case <-tc.queue.notify:
					case <-time.After(time.Second):
						t.Fatalf("timed out waiting for producer to flush queue")
					}
					tc.queue.mu.Lock()
					got := len(tc.queue.batches)
					tc.queue.mu.Unlock()
					if got != tc.expectBatches {
						t.Fatalf("expected %d batches in queue, got %d", tc.expectBatches, got)
					}
				}
			}
		})
	}
}
