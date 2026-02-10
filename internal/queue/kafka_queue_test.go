package queue

import (
	"context"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/config"
	"github.com/lechuhuuha/log_forge/model"
)

func TestCompressionCodec(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{name: "none empty", in: ""},
		{name: "none literal", in: "none"},
		{name: "gzip", in: "gzip"},
		{name: "snappy", in: "snappy"},
		{name: "lz4", in: "lz4"},
		{name: "zstd", in: "zstd"},
		{name: "zstandard", in: "zstandard"},
		{name: "unknown", in: "unknown"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_ = compressionCodec(tc.in)
		})
	}
}

func TestNewKafkaLogQueue(t *testing.T) {
	cases := []struct {
		name    string
		cfg     config.KafkaSettings
		wantErr bool
		check   func(t *testing.T, q *KafkaLogQueue)
	}{
		{
			name:    "requires brokers",
			cfg:     config.KafkaSettings{},
			wantErr: true,
		},
		{
			name: "applies defaults",
			cfg: config.KafkaSettings{
				Brokers: []string{"localhost:9092"},
			},
			check: func(t *testing.T, q *KafkaLogQueue) {
				if q.readerCfg.Topic != "logs" {
					t.Fatalf("expected default topic logs, got %q", q.readerCfg.Topic)
				}
				if q.readerCfg.GroupID != "logs-consumer-group" {
					t.Fatalf("expected default group id, got %q", q.readerCfg.GroupID)
				}
				if q.consumers != 1 {
					t.Fatalf("expected default consumers 1, got %d", q.consumers)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			q, err := NewKafkaLogQueue(tc.cfg, nil)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewKafkaLogQueue returned error: %v", err)
			}
			t.Cleanup(func() {
				_ = q.Close()
			})
			if tc.check != nil {
				tc.check(t, q)
			}
		})
	}
}

func TestKafkaLogQueue_Behavior(t *testing.T) {
	cases := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "enqueue batch empty slice",
			run: func(t *testing.T) {
				q := &KafkaLogQueue{}
				if err := q.EnqueueBatch(context.Background(), nil); err != nil {
					t.Fatalf("EnqueueBatch returned error for empty slice: %v", err)
				}
			},
		},
		{
			name: "start consumers nil handler",
			run: func(t *testing.T) {
				q := &KafkaLogQueue{}
				if err := q.StartConsumers(context.Background(), nil); err == nil {
					t.Fatal("expected error for nil handler")
				}
			},
		},
		{
			name: "start consumers canceled context",
			run: func(t *testing.T) {
				q, err := NewKafkaLogQueue(config.KafkaSettings{
					Brokers:   []string{"localhost:9092"},
					Topic:     "logs",
					GroupID:   "group",
					Consumers: 1,
				}, nil)
				if err != nil {
					t.Fatalf("NewKafkaLogQueue returned error: %v", err)
				}
				defer func() { _ = q.Close() }()

				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				if err := q.StartConsumers(ctx, func(context.Context, model.ConsumedMessage) {}); err != nil {
					t.Fatalf("StartConsumers returned error: %v", err)
				}

				time.Sleep(20 * time.Millisecond)
			},
		},
		{
			name: "close nil receiver",
			run: func(t *testing.T) {
				var q *KafkaLogQueue
				if err := q.Close(); err != nil {
					t.Fatalf("expected nil error for nil receiver, got %v", err)
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
