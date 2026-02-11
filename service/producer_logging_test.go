package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/model"
)

type producerLogEntry struct {
	msg    string
	fields []loggerpkg.Field
}

type producerCaptureLogger struct {
	mu     sync.Mutex
	warns  []producerLogEntry
	errors []producerLogEntry
}

func (l *producerCaptureLogger) Info(msg string, fields ...loggerpkg.Field) {}

func (l *producerCaptureLogger) Warn(msg string, fields ...loggerpkg.Field) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, producerLogEntry{msg: msg, fields: cloneProducerFields(fields)})
}

func (l *producerCaptureLogger) Error(msg string, fields ...loggerpkg.Field) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, producerLogEntry{msg: msg, fields: cloneProducerFields(fields)})
}

func (l *producerCaptureLogger) Debug(msg string, fields ...loggerpkg.Field) {}

func (l *producerCaptureLogger) Fatal(msg string, fields ...loggerpkg.Field) {}

func cloneProducerFields(fields []loggerpkg.Field) []loggerpkg.Field {
	cp := make([]loggerpkg.Field, len(fields))
	copy(cp, fields)
	return cp
}

func TestProducerServiceTransientFailureLogging(t *testing.T) {
	records := []model.LogRecord{{
		Timestamp: time.Now().UTC(),
		Path:      "/logs",
		UserAgent: "test",
	}}
	permanentErr := errors.New("payload encoding invalid")

	cases := []struct {
		name       string
		queueErr   error
		calls      int
		beforeCall func(idx int, p *ProducerService)
		wantErr    error
		wantWarns  int
		wantErrors int
		assert     func(t *testing.T, logs *producerCaptureLogger)
	}{
		{
			name:       "transient failures are suppressed",
			queueErr:   context.DeadlineExceeded,
			calls:      5,
			wantErr:    context.DeadlineExceeded,
			wantWarns:  1,
			wantErrors: 0,
			assert: func(t *testing.T, logs *producerCaptureLogger) {
				t.Helper()
				if logs.warns[0].msg != "failed to enqueue logs (transient, suppressed)" {
					t.Fatalf("unexpected warn message: %s", logs.warns[0].msg)
				}
			},
		},
		{
			name:       "non transient failures stay as error logs",
			queueErr:   permanentErr,
			calls:      3,
			wantErr:    permanentErr,
			wantWarns:  0,
			wantErrors: 3,
			assert: func(t *testing.T, logs *producerCaptureLogger) {
				t.Helper()
				if logs.errors[0].msg != "failed to enqueue logs" {
					t.Fatalf("unexpected error message: %s", logs.errors[0].msg)
				}
			},
		},
		{
			name:     "suppressed warning logs again after interval",
			queueErr: context.DeadlineExceeded,
			calls:    3,
			beforeCall: func(idx int, p *ProducerService) {
				if idx == 2 {
					p.lastTransientErrorLogNs.Store(time.Now().UTC().Add(-2 * producerTransientLogInterval).UnixNano())
				}
			},
			wantErr:    context.DeadlineExceeded,
			wantWarns:  2,
			wantErrors: 0,
			assert: func(t *testing.T, logs *producerCaptureLogger) {
				t.Helper()
				events, ok := producerFieldUint64(logs.warns[1].fields, "events")
				if !ok {
					t.Fatal("expected events field in warning log")
				}
				if events < 2 {
					t.Fatalf("expected suppressed events >= 2, got %d", events)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			logs := &producerCaptureLogger{}
			producer := NewProducerService(&mockQueue{err: tc.queueErr}, logs, &ProducerConfig{
				QueueBufferSize:         1,
				Workers:                 1,
				WriteTimeout:            time.Millisecond,
				MaxRetries:              0,
				CircuitFailureThreshold: 999999,
				CircuitCooldown:         time.Second,
			})
			t.Cleanup(producer.Close)
			producer.Start()

			for i := 0; i < tc.calls; i++ {
				if tc.beforeCall != nil {
					tc.beforeCall(i, producer)
				}
				err := producer.EnqueueSync(context.Background(), records)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("unexpected error: got=%v want=%v", err, tc.wantErr)
				}
			}

			if len(logs.warns) != tc.wantWarns {
				t.Fatalf("expected %d warn logs, got %d", tc.wantWarns, len(logs.warns))
			}
			if len(logs.errors) != tc.wantErrors {
				t.Fatalf("expected %d error logs, got %d", tc.wantErrors, len(logs.errors))
			}
			if tc.assert != nil {
				tc.assert(t, logs)
			}
		})
	}
}

func producerFieldUint64(fields []loggerpkg.Field, key string) (uint64, bool) {
	for _, field := range fields {
		if field.Key != key {
			continue
		}
		switch v := field.Value.(type) {
		case uint64:
			return v, true
		case int:
			if v < 0 {
				return 0, false
			}
			return uint64(v), true
		case int64:
			if v < 0 {
				return 0, false
			}
			return uint64(v), true
		}
	}
	return 0, false
}
