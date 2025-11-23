package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/model"
)

type benchStore struct {
	mu    sync.Mutex
	count int
}

func (s *benchStore) SaveBatch(ctx context.Context, records []model.LogRecord) error {
	s.mu.Lock()
	s.count += len(records)
	s.mu.Unlock()
	return nil
}

func BenchmarkConsumerBatchWriter_AddAndFlush(b *testing.B) {
	store := &benchStore{}
	cfg := ConsumerBatchConfig{
		FlushSize:      1,           // flush on every add to exercise the hot path
		FlushInterval:  time.Hour,   // avoid timer-driven flush during the bench
		PersistTimeout: time.Second, // small, but unused because SaveBatch is fast
	}
	writer := newConsumerWriter(context.Background(), store, cfg, nil)
	b.Cleanup(writer.Close)

	msg := model.ConsumedMessage{
		Record: model.LogRecord{
			Timestamp: time.Now().UTC(),
			Path:      "/bench",
			UserAgent: "ua",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		writer.Add(msg)
	}
}
