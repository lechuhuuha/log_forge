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

type enqueueErrProducer struct {
	err error
}

func (p enqueueErrProducer) Enqueue(ctx context.Context, batch []model.LogRecord) error {
	return p.err
}

func (p enqueueErrProducer) EnqueueSync(ctx context.Context, batch []model.LogRecord) error {
	return p.err
}

type capturedLogEntry struct {
	msg    string
	fields []loggerpkg.Field
}

type capturedLogger struct {
	mu     sync.Mutex
	warns  []capturedLogEntry
	errors []capturedLogEntry
}

func (l *capturedLogger) Info(msg string, fields ...loggerpkg.Field) {}

func (l *capturedLogger) Warn(msg string, fields ...loggerpkg.Field) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, capturedLogEntry{msg: msg, fields: cloneFields(fields)})
}

func (l *capturedLogger) Error(msg string, fields ...loggerpkg.Field) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, capturedLogEntry{msg: msg, fields: cloneFields(fields)})
}

func (l *capturedLogger) Debug(msg string, fields ...loggerpkg.Field) {}

func (l *capturedLogger) Fatal(msg string, fields ...loggerpkg.Field) {}

func cloneFields(fields []loggerpkg.Field) []loggerpkg.Field {
	cp := make([]loggerpkg.Field, len(fields))
	copy(cp, fields)
	return cp
}

func TestIngestionServiceQueueErrorLogging(t *testing.T) {
	records := []model.LogRecord{{
		Timestamp: time.Now().UTC(),
		Path:      "/logs",
		UserAgent: "test",
	}}
	permanentErr := errors.New("kafka write failed")

	cases := []struct {
		name       string
		producer   Producer
		calls      int
		beforeCall func(idx int, svc *IngestionService)
		wantErr    error
		wantWarns  int
		wantErrors int
		assert     func(t *testing.T, logs *capturedLogger)
	}{
		{
			name:       "transient queue errors are throttled",
			producer:   enqueueErrProducer{err: ErrProducerCircuitOpen},
			calls:      5,
			wantErr:    ErrProducerCircuitOpen,
			wantWarns:  1,
			wantErrors: 0,
			assert: func(t *testing.T, logs *capturedLogger) {
				t.Helper()
				if logs.warns[0].msg != "failed to enqueue logs (transient, suppressed)" {
					t.Fatalf("unexpected warn message: %s", logs.warns[0].msg)
				}
			},
		},
		{
			name:       "non transient queue errors stay at error level",
			producer:   enqueueErrProducer{err: permanentErr},
			calls:      3,
			wantErr:    permanentErr,
			wantWarns:  0,
			wantErrors: 3,
			assert: func(t *testing.T, logs *capturedLogger) {
				t.Helper()
				if logs.errors[0].msg != "failed to enqueue logs" {
					t.Fatalf("unexpected error message: %s", logs.errors[0].msg)
				}
			},
		},
		{
			name:     "transient warning logs again after interval window",
			producer: enqueueErrProducer{err: ErrProducerCircuitOpen},
			calls:    3,
			beforeCall: func(idx int, svc *IngestionService) {
				// Force the third call to pass the throttle window.
				if idx == 2 {
					svc.lastTransientQueueLogNs.Store(time.Now().UTC().Add(-2 * ingestionTransientLogInterval).UnixNano())
				}
			},
			wantErr:    ErrProducerCircuitOpen,
			wantWarns:  2,
			wantErrors: 0,
			assert: func(t *testing.T, logs *capturedLogger) {
				t.Helper()
				events, ok := fieldUint64(logs.warns[1].fields, "events")
				if !ok {
					t.Fatal("expected events field in throttled warning")
				}
				if events < 2 {
					t.Fatalf("expected second warning to report >=2 events, got %d", events)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			logs := &capturedLogger{}
			svc := NewIngestionService(nil, tc.producer, ModeQueue, false, logs)

			for i := 0; i < tc.calls; i++ {
				if tc.beforeCall != nil {
					tc.beforeCall(i, svc)
				}
				err := svc.ProcessBatch(context.Background(), records)
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

func fieldUint64(fields []loggerpkg.Field, key string) (uint64, bool) {
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
