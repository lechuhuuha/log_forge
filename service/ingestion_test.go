package service

import (
	"context"
	"sync"
	"testing"
	"time"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/model"
)

type mockStore struct {
	batches [][]model.LogRecord
	err     error
}

func (m *mockStore) SaveBatch(ctx context.Context, records []model.LogRecord) error {
	if m.err == nil {
		cp := make([]model.LogRecord, len(records))
		copy(cp, records)
		m.batches = append(m.batches, cp)
	}
	return m.err
}

type mockProducer struct {
	mu      sync.Mutex
	batches [][]model.LogRecord
	err     error
}

func (m *mockProducer) Enqueue(ctx context.Context, records []model.LogRecord) error {
	if m.err == nil {
		m.mu.Lock()
		cp := make([]model.LogRecord, len(records))
		copy(cp, records)
		m.batches = append(m.batches, cp)
		m.mu.Unlock()
	}
	return m.err
}

func TestIngestionService_ProcessBatch(t *testing.T) {
	now := time.Now()
	records := []model.LogRecord{{
		Timestamp: now,
		Path:      "/home",
		UserAgent: "ua",
	}}

	cases := []struct {
		name          string
		mode          PipelineMode
		store         *mockStore
		producer      *mockProducer
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
			name:          "queue mode uses producer",
			mode:          ModeQueue,
			producer:      &mockProducer{},
			expectBatches: 1,
		},
		{
			name:      "direct mode propagates errors",
			mode:      ModeDirect,
			store:     &mockStore{err: context.DeadlineExceeded},
			expectErr: true,
		},
		{
			name:      "queue mode propagates errors",
			mode:      ModeQueue,
			producer:  &mockProducer{err: context.DeadlineExceeded},
			expectErr: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc := NewIngestionService(tc.store, tc.producer, tc.mode, loggerpkg.NewNop())
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
				if tc.producer != nil {
					tc.producer.mu.Lock()
					got := len(tc.producer.batches)
					tc.producer.mu.Unlock()
					if got != tc.expectBatches {
						t.Fatalf("expected %d batches in producer, got %d", tc.expectBatches, got)
					}
				}
			}
		})
	}
}
