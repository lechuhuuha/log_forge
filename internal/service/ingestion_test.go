package service

import (
	"context"
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
	batches [][]domain.LogRecord
	err     error
}

func (m *mockQueue) EnqueueBatch(ctx context.Context, records []domain.LogRecord) error {
	if m.err == nil {
		cp := make([]domain.LogRecord, len(records))
		copy(cp, records)
		m.batches = append(m.batches, cp)
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
	q := &mockQueue{}
	svc := NewIngestionService(nil, q, ModeQueue, loggerpkg.NewNop())
	records := []domain.LogRecord{{
		Timestamp: time.Now(),
		Path:      "/api",
		UserAgent: "ua",
	}}

	if err := svc.ProcessBatch(context.Background(), records); err != nil {
		t.Fatalf("ProcessBatch returned error: %v", err)
	}
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
