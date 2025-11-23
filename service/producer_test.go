package service

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/internal/domain"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

type mockQueue struct {
	mu      sync.Mutex
	batches [][]domain.LogRecord
	err     error
	notify  chan struct{}
	callCnt int
}

func (m *mockQueue) EnqueueBatch(ctx context.Context, records []domain.LogRecord) error {
	m.mu.Lock()
	m.callCnt++
	m.mu.Unlock()
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

func (m *mockQueue) StartConsumers(ctx context.Context, handler func(context.Context, domain.ConsumedMessage)) error {
	return nil
}

func TestProducerService_Enqueue(t *testing.T) {
	records := []domain.LogRecord{{
		Timestamp: time.Now(),
		Path:      "/home",
		UserAgent: "ua",
	}}
	q := &mockQueue{notify: make(chan struct{}, 1)}
	cfg := &ProducerConfig{QueueBufferSize: 4, Workers: 1, WriteTimeout: 5 * time.Millisecond}
	producer := NewProducerService(q, loggerpkg.NewNop(), cfg)
	t.Cleanup(producer.Close)
	producer.Start()

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
}

func TestProducerService_WritesDLQOnFailure(t *testing.T) {
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
	producer.Start()

	records := []domain.LogRecord{{
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
}
