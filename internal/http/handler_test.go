package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lechuhuuha/log_forge/internal/domain"
	"github.com/lechuhuuha/log_forge/internal/service"
)

type mockStore struct {
	callCount int
	batches   [][]domain.LogRecord
	err       error
}

func (m *mockStore) SaveBatch(ctx context.Context, records []domain.LogRecord) error {
	m.callCount++
	if m.err == nil {
		cp := make([]domain.LogRecord, len(records))
		copy(cp, records)
		m.batches = append(m.batches, cp)
	}
	return m.err
}

func newHandlerWithStore(store domain.LogStore) *Handler {
	ingestion := service.NewIngestionService(store, nil, service.ModeDirect, nil, nil)
	return NewHandler(ingestion, nil)
}

func TestHandleLogs_JSONSuccess(t *testing.T) {
	store := &mockStore{}
	handler := newHandlerWithStore(store)

	body := `[{"timestamp":"2023-01-02T03:04:05+02:00","path":"/home","userAgent":" UA "}]`
	req := httptest.NewRequest(http.MethodPost, "/logs", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.handleLogs(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}
	if len(store.batches) != 1 || len(store.batches[0]) != 1 {
		t.Fatalf("expected 1 batch with 1 record, got %d batches", len(store.batches))
	}

	rec0 := store.batches[0][0]
	if rec0.Path != "/home" {
		t.Fatalf("expected path /home, got %s", rec0.Path)
	}
	if rec0.UserAgent != "UA" {
		t.Fatalf("expected trimmed userAgent UA, got %s", rec0.UserAgent)
	}
	// Timestamp should be normalized to UTC.
	expected := time.Date(2023, 1, 2, 1, 4, 5, 0, time.UTC)
	if !rec0.Timestamp.Equal(expected) {
		t.Fatalf("expected timestamp %v, got %v", expected, rec0.Timestamp)
	}
}

func TestHandleLogs_JSONInvalidPayload(t *testing.T) {
	store := &mockStore{}
	handler := newHandlerWithStore(store)

	startInvalid := counterValue(t, "invalid_requests_total")

	req := httptest.NewRequest(http.MethodPost, "/logs", strings.NewReader(`{invalid`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.handleLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
	if len(store.batches) != 0 {
		t.Fatalf("expected no batches stored, got %d", len(store.batches))
	}

	if delta := counterValue(t, "invalid_requests_total") - startInvalid; delta != 1 {
		t.Fatalf("expected invalid_requests_total to increase by 1, got %.0f", delta)
	}
}

func TestHandleLogs_CSVSuccess(t *testing.T) {
	store := &mockStore{}
	handler := newHandlerWithStore(store)

	csvBody := "timestamp,path,userAgent\n" +
		"2023-03-04T10:00:00Z,/api ,test-agent\n" +
		"2023-03-04T11:00:00Z,/health, second-agent "
	req := httptest.NewRequest(http.MethodPost, "/logs", strings.NewReader(csvBody))
	req.Header.Set("Content-Type", "text/csv")
	rec := httptest.NewRecorder()

	handler.handleLogs(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rec.Code)
	}
	if len(store.batches) != 1 || len(store.batches[0]) != 2 {
		t.Fatalf("expected 1 batch with 2 records, got %d batches with %d records", len(store.batches), len(store.batches[0]))
	}
	if store.batches[0][0].Path != "/api" {
		t.Fatalf("expected trimmed path /api, got %s", store.batches[0][0].Path)
	}
	if store.batches[0][1].UserAgent != "second-agent" {
		t.Fatalf("expected trimmed userAgent second-agent, got %s", store.batches[0][1].UserAgent)
	}
}

func TestHandleLogs_CSVMissingColumns(t *testing.T) {
	store := &mockStore{}
	handler := newHandlerWithStore(store)

	req := httptest.NewRequest(http.MethodPost, "/logs", strings.NewReader("timestamp,path\n2023-03-04T10:00:00Z,/onlytwo"))
	req.Header.Set("Content-Type", "text/csv")
	rec := httptest.NewRecorder()

	handler.handleLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
	if len(store.batches) != 0 {
		t.Fatalf("expected no batches stored, got %d", len(store.batches))
	}
}

func TestHandleLogs_ValidationError(t *testing.T) {
	store := &mockStore{}
	handler := newHandlerWithStore(store)

	startInvalid := counterValue(t, "invalid_requests_total")

	body := `[{"timestamp":"2023-01-02T03:04:05Z","path":"","userAgent":"ua"}]`
	req := httptest.NewRequest(http.MethodPost, "/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.handleLogs(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
	if len(store.batches) != 0 {
		t.Fatalf("expected no batches stored, got %d", len(store.batches))
	}
	if delta := counterValue(t, "invalid_requests_total") - startInvalid; delta != 1 {
		t.Fatalf("expected invalid_requests_total to increase by 1, got %.0f", delta)
	}
}

func TestHandleLogs_ProcessBatchError(t *testing.T) {
	store := &mockStore{err: context.DeadlineExceeded}
	handler := newHandlerWithStore(store)

	body := `[{"timestamp":"2023-01-02T03:04:05Z","path":"/x","userAgent":"ua"}]`
	req := httptest.NewRequest(http.MethodPost, "/logs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.handleLogs(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}
	if store.callCount != 1 {
		t.Fatalf("expected SaveBatch to be invoked once, got %d", store.callCount)
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
