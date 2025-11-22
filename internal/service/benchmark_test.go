package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/internal/domain"
	"github.com/lechuhuuha/log_forge/util"
)

// benchmarkStore is a lightweight LogStore used for benchmarks.
type benchmarkStore struct{}

func (benchmarkStore) SaveBatch(ctx context.Context, records []domain.LogRecord) error {
	return nil
}

type benchmarkQueue struct {
	mu    sync.Mutex
	count int
	err   error
}

func (q *benchmarkQueue) EnqueueBatch(ctx context.Context, records []domain.LogRecord) error {
	q.mu.Lock()
	q.count += len(records)
	q.mu.Unlock()
	return q.err
}

func (q *benchmarkQueue) StartConsumers(ctx context.Context, handler func(context.Context, domain.LogRecord)) error {
	return nil
}

func BenchmarkAggregationAggregateAll(b *testing.B) {
	logsDir := b.TempDir()
	analyticsDir := b.TempDir()

	// Seed one hour of NDJSON logs.
	hour := time.Date(2023, 3, 4, 10, 0, 0, 0, time.UTC)
	if err := writeHourFile(logsDir, hour, 1000); err != nil {
		b.Fatalf("write log file: %v", err)
	}

	a := NewAggregationService(logsDir, analyticsDir, time.Minute, nil)
	ctx := context.Background()
	summaryPath := filepath.Join(analyticsDir, hour.Format(util.DateLayout), "summary_"+hour.Format(util.HourLayout)+".json")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = os.Remove(summaryPath)
		if err := a.AggregateAll(ctx); err != nil {
			b.Fatalf("aggregate all: %v", err)
		}
	}
}

func BenchmarkIngestionProcessBatchDirect(b *testing.B) {
	store := benchmarkStore{}
	svc := NewIngestionService(store, nil, ModeDirect, nil, nil)
	records := []domain.LogRecord{{
		Timestamp: time.Now().UTC(),
		Path:      "/bench",
		UserAgent: "ua",
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := svc.ProcessBatch(context.Background(), records); err != nil {
			b.Fatalf("ProcessBatch: %v", err)
		}
	}
}

func BenchmarkIngestionProcessBatchQueue(b *testing.B) {
	queue := &benchmarkQueue{}
	cfg := &IngestionConfig{
		QueueBufferSize:      1024,
		ProducerWorkers:      4,
		ProducerWriteTimeout: time.Second,
	}
	svc := NewIngestionService(nil, queue, ModeQueue, nil, cfg)
	b.Cleanup(svc.Close)

	records := []domain.LogRecord{{
		Timestamp: time.Now().UTC(),
		Path:      "/bench",
		UserAgent: "ua",
	}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := svc.ProcessBatch(context.Background(), records); err != nil {
			b.Fatalf("ProcessBatch: %v", err)
		}
	}
	b.StopTimer()

	// Ensure producers drained the channel.
	deadline := time.Now().Add(2 * time.Second)
	for {
		queue.mu.Lock()
		count := queue.count
		queue.mu.Unlock()
		if count >= b.N || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func writeHourFile(logsDir string, ts time.Time, lines int) error {
	dateDir := ts.Format(util.DateLayout)
	hourFile := ts.Format(util.HourLayout) + ".log.json"
	dir := filepath.Join(logsDir, dateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, hourFile)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	rec := domain.LogRecord{
		Timestamp: ts,
		Path:      "/path",
		UserAgent: "agent",
	}
	enc := json.NewEncoder(f)
	for i := 0; i < lines; i++ {
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	return nil
}
