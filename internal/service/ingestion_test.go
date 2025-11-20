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

func TestIngestionService_DirectModeUsesStore(t *testing.T) {
	store := &mockStore{}
	svc := NewIngestionService(store, nil, ModeDirect, loggerpkg.NewNop())
	records := []domain.LogRecord{{
		Timestamp: time.Now(),
		Path:      "/home",
		UserAgent: "ua",
	}}

	if err := svc.ProcessBatch(context.Background(), records); err != nil {
		t.Fatalf("ProcessBatch returned error: %v", err)
	}
	if len(store.batches) != 1 {
		t.Fatalf("expected store to receive 1 batch, got %d", len(store.batches))
	}
}

func TestIngestionService_QueueModeUsesQueue(t *testing.T) {
	notify := make(chan struct{}, 1)
	q := &mockQueue{notify: notify}
	svc := NewIngestionService(nil, q, ModeQueue, loggerpkg.NewNop())
	t.Cleanup(svc.Close)
	records := []domain.LogRecord{{
		Timestamp: time.Now(),
		Path:      "/api",
		UserAgent: "ua",
	}}

	if err := svc.ProcessBatch(context.Background(), records); err != nil {
		t.Fatalf("ProcessBatch returned error: %v", err)
	}
	select {
	case <-notify:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for producer to flush queue")
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.batches) != 1 {
		t.Fatalf("expected queue to receive 1 batch, got %d", len(q.batches))
	}
}

func TestIngestionService_PropagatesErrors(t *testing.T) {
	store := &mockStore{err: context.DeadlineExceeded}
	svc := NewIngestionService(store, nil, ModeDirect, loggerpkg.NewNop())
	records := []domain.LogRecord{{
		Timestamp: time.Now(),
		Path:      "/error",
		UserAgent: "ua",
	}}

	err := svc.ProcessBatch(context.Background(), records)
	if err == nil {
		t.Fatalf("expected error but got nil")
	}
}
