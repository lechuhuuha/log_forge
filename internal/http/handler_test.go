package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lechuhuuha/log_forge/model"
	"github.com/lechuhuuha/log_forge/repo"
	"github.com/lechuhuuha/log_forge/service"
)

type mockStore struct {
	callCount int
	batches   [][]model.LogRecord
	err       error
}

func (m *mockStore) SaveBatch(ctx context.Context, records []model.LogRecord) error {
	m.callCount++
	if m.err == nil {
		cp := make([]model.LogRecord, len(records))
		copy(cp, records)
		m.batches = append(m.batches, cp)
	}
	return m.err
}

func newHandlerWithStore(store repo.Repository) *Handler {
	ingestion := service.NewIngestionService(store, nil, service.ModeDirect, false, nil)
	return NewHandler(ingestion, nil)
}

func TestHandleLogs(t *testing.T) {
	type expect struct {
		status          int
		batches         int
		recordsInBatch  int
		invalidDelta    int
		callCount       int
		firstPath       string
		firstUserAgent  string
		expectTimestamp time.Time
	}

	cases := []struct {
		name        string
		contentType string
		body        string
		storeErr    error
		expect      expect
	}{
		{
			name:        "json success trims and normalizes",
			contentType: "application/json",
			body:        `[{"timestamp":"2023-01-02T03:04:05+02:00","path":"/home","userAgent":" UA "}]`,
			expect: expect{
				status:          http.StatusAccepted,
				batches:         1,
				recordsInBatch:  1,
				firstPath:       "/home",
				firstUserAgent:  "UA",
				expectTimestamp: time.Date(2023, 1, 2, 1, 4, 5, 0, time.UTC),
			},
		},
		{
			name:        "json invalid payload increments invalid counter",
			contentType: "application/json",
			body:        `{invalid`,
			expect: expect{
				status:       http.StatusBadRequest,
				batches:      0,
				invalidDelta: 1,
			},
		},
		{
			name:        "csv success handles header and trims",
			contentType: "text/csv",
			body: "timestamp,path,userAgent\n" +
				"2023-03-04T10:00:00Z,/api ,test-agent\n" +
				"2023-03-04T11:00:00Z,/health, second-agent ",
			expect: expect{
				status:         http.StatusAccepted,
				batches:        1,
				recordsInBatch: 2,
				firstPath:      "/api",
				firstUserAgent: "test-agent",
			},
		},
		{
			name:        "csv missing columns returns bad request",
			contentType: "text/csv",
			body:        "timestamp,path\n2023-03-04T10:00:00Z,/onlytwo",
			expect: expect{
				status:       http.StatusBadRequest,
				batches:      0,
				invalidDelta: 1,
			},
		},
		{
			name:        "validation error increments invalid counter",
			contentType: "application/json",
			body:        `[{"timestamp":"2023-01-02T03:04:05Z","path":"","userAgent":"ua"}]`,
			expect: expect{
				status:       http.StatusBadRequest,
				batches:      0,
				invalidDelta: 1,
			},
		},
		{
			name:        "ingestion error returns service unavailable",
			contentType: "application/json",
			body:        `[{"timestamp":"2023-01-02T03:04:05Z","path":"/x","userAgent":"ua"}]`,
			storeErr:    context.DeadlineExceeded,
			expect: expect{
				status:       http.StatusServiceUnavailable,
				callCount:    1,
				batches:      0,
				invalidDelta: 0,
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			store := &mockStore{err: tc.storeErr}
			handler := newHandlerWithStore(store)

			startInvalid := counterValue(t, "invalid_requests_total")

			var bodyReader io.Reader = strings.NewReader(tc.body)
			if strings.Contains(tc.contentType, "json") {
				bodyReader = bytes.NewBufferString(tc.body)
			}

			req := httptest.NewRequest(http.MethodPost, "/logs", bodyReader)
			req.Header.Set("Content-Type", tc.contentType)
			rec := httptest.NewRecorder()

			handler.handleLogs(rec, req)

			if rec.Code != tc.expect.status {
				t.Fatalf("expected status %d, got %d", tc.expect.status, rec.Code)
			}
			if store.callCount != tc.expect.callCount && tc.storeErr != nil {
				t.Fatalf("expected SaveBatch to be invoked %d times, got %d", tc.expect.callCount, store.callCount)
			}
			if len(store.batches) != tc.expect.batches {
				t.Fatalf("expected %d batches, got %d", tc.expect.batches, len(store.batches))
			}
			if tc.expect.recordsInBatch > 0 && len(store.batches) > 0 && len(store.batches[0]) != tc.expect.recordsInBatch {
				t.Fatalf("expected %d records in batch, got %d", tc.expect.recordsInBatch, len(store.batches[0]))
			}
			if len(store.batches) > 0 && len(store.batches[0]) > 0 {
				rec0 := store.batches[0][0]
				if tc.expect.firstPath != "" && rec0.Path != tc.expect.firstPath {
					t.Fatalf("expected path %s, got %s", tc.expect.firstPath, rec0.Path)
				}
				if tc.expect.firstUserAgent != "" && rec0.UserAgent != tc.expect.firstUserAgent {
					t.Fatalf("expected userAgent %s, got %s", tc.expect.firstUserAgent, rec0.UserAgent)
				}
				if !tc.expect.expectTimestamp.IsZero() && !rec0.Timestamp.Equal(tc.expect.expectTimestamp) {
					t.Fatalf("expected timestamp %v, got %v", tc.expect.expectTimestamp, rec0.Timestamp)
				}
			}

			if delta := counterValue(t, "invalid_requests_total") - startInvalid; delta != float64(tc.expect.invalidDelta) {
				t.Fatalf("expected invalid_requests_total delta %d, got %.0f", tc.expect.invalidDelta, delta)
			}
		})
	}
}

type errProducer struct {
	err error
}

func (p errProducer) Enqueue(ctx context.Context, records []model.LogRecord) error {
	return p.err
}

func (p errProducer) EnqueueSync(ctx context.Context, records []model.LogRecord) error {
	return p.err
}

func TestHandleLogsQueueErrors(t *testing.T) {
	cases := []struct {
		name       string
		producerErr error
		wantStatus int
		wantRetry  bool
	}{
		{
			name:       "queue full maps to 429",
			producerErr: service.ErrQueueFull,
			wantStatus: http.StatusTooManyRequests,
			wantRetry:  true,
		},
		{
			name:       "producer stopped maps to 503",
			producerErr: service.ErrProducerStopped,
			wantStatus: http.StatusServiceUnavailable,
			wantRetry:  true,
		},
		{
			name:       "deadline exceeded maps to 503",
			producerErr: context.DeadlineExceeded,
			wantStatus: http.StatusServiceUnavailable,
			wantRetry:  true,
		},
		{
			name:       "producer not started maps to 503",
			producerErr: service.ErrProducerNotStarted,
			wantStatus: http.StatusServiceUnavailable,
			wantRetry:  true,
		},
	}

	body := `[{"timestamp":"2023-01-02T03:04:05Z","path":"/x","userAgent":"ua"}]`
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ingestion := service.NewIngestionService(nil, errProducer{err: tc.producerErr}, service.ModeQueue, false, nil)
			handler := NewHandler(ingestion, nil)

			req := httptest.NewRequest(http.MethodPost, "/logs", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.handleLogs(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d", tc.wantStatus, rec.Code)
			}
			if tc.wantRetry {
				if got := rec.Header().Get("Retry-After"); got == "" {
					t.Fatalf("expected Retry-After header to be set")
				}
			}
		})
	}
}

func counterValue(t *testing.T, name string) float64 {
	t.Helper()
	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() == name {
			if len(mf.GetMetric()) > 0 && mf.GetMetric()[0].Counter != nil {
				return mf.GetMetric()[0].Counter.GetValue()
			}
		}
	}
	return 0
}
