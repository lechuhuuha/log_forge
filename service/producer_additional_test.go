package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/model"
)

func TestProducerServiceAdditionalPaths(t *testing.T) {
	record := []model.LogRecord{{
		Timestamp: time.Now().UTC(),
		Path:      "/x",
		UserAgent: "ua",
	}}

	cases := []struct {
		name   string
		run    func() error
		assert func(t *testing.T, err error)
	}{
		{
			name: "enqueue not started",
			run: func() error {
				p := NewProducerService(&mockQueue{}, nil, &ProducerConfig{QueueBufferSize: 1, Workers: 1})
				return p.Enqueue(context.Background(), record)
			},
			assert: func(t *testing.T, err error) {
				if !errors.Is(err, ErrProducerNotStarted) {
					t.Fatalf("expected ErrProducerNotStarted, got %v", err)
				}
			},
		},
		{
			name: "enqueue closed",
			run: func() error {
				p := NewProducerService(&mockQueue{}, nil, &ProducerConfig{QueueBufferSize: 1, Workers: 1})
				p.StartAsync()
				p.Close()
				return p.Enqueue(context.Background(), record)
			},
			assert: func(t *testing.T, err error) {
				if !errors.Is(err, ErrProducerStopped) {
					t.Fatalf("expected ErrProducerStopped, got %v", err)
				}
			},
		},
		{
			name: "enqueue context canceled when buffer full",
			run: func() error {
				p := NewProducerService(&mockQueue{}, nil, &ProducerConfig{QueueBufferSize: 1, Workers: 1})
				// Mark async path as started without launching workers so the channel stays full.
				p.started.Store(true)
				p.workCh <- []model.LogRecord{{
					Timestamp: time.Now().UTC(),
					Path:      "/queued",
					UserAgent: "ua",
				}}

				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				err := p.Enqueue(ctx, record)
				p.Close()
				return err
			},
			assert: func(t *testing.T, err error) {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context.Canceled, got %v", err)
				}
			},
		},
		{
			name: "enqueue sync works without async start",
			run: func() error {
				p := NewProducerService(&mockQueue{}, nil, &ProducerConfig{QueueBufferSize: 1, Workers: 1})
				defer p.Close()
				return p.EnqueueSync(context.Background(), record)
			},
			assert: func(t *testing.T, err error) {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			tc.assert(t, err)
		})
	}
}

func TestProducerServiceRetryPaths(t *testing.T) {
	cases := []struct {
		name    string
		ctx     func() context.Context
		wantErr error
	}{
		{
			name: "retry fails when context canceled",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantErr: context.Canceled,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := NewProducerService(&mockQueue{err: errors.New("queue failed")}, nil, &ProducerConfig{
				MaxRetries:   5,
				RetryBackoff: time.Second,
			})
			err := p.tryEnqueueWithRetry(tc.ctx(), []model.LogRecord{{
				Timestamp: time.Now().UTC(),
				Path:      "/x",
				UserAgent: "ua",
			}}, 1)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("unexpected retry error: got=%v want=%v", err, tc.wantErr)
			}
		})
	}
}
