package httpapi

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lechuhuuha/log_forge/internal/metrics"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/model"
	"github.com/lechuhuuha/log_forge/service"
)

const (
	maxRequestBodyBytes   = 2 << 20 // 2 MiB
	defaultRequestTimeout = 5 * time.Second
	transientLogInterval  = 5 * time.Second
)

var errUnsupportedContentType = errors.New("unsupported content-type")

// Handler wires HTTP endpoints to services.
type Handler struct {
	ingestion          *service.IngestionService
	logger             loggerpkg.Logger
	requestTimeout     time.Duration
	lastTransientLogNs atomic.Int64
}

// NewHandler builds the HTTP handler set.
func NewHandler(ingestion *service.IngestionService, logr loggerpkg.Logger) *Handler {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	return &Handler{
		ingestion:      ingestion,
		logger:         logr,
		requestTimeout: defaultRequestTimeout,
	}
}

// WithRequestTimeout overrides the per-request processing timeout used for /logs.
func (h *Handler) WithRequestTimeout(timeout time.Duration) *Handler {
	if h == nil {
		return h
	}
	if timeout > 0 {
		h.requestTimeout = timeout
	}
	return h
}

// RegisterRoutes attaches the HTTP endpoints to the provided mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/logs", h.handleLogs)
}

func (h *Handler) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.ingestion == nil {
		w.Header().Set("Retry-After", "1")
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	defer r.Body.Close()

	records, err := h.decodeRecords(r)
	if err != nil {
		metrics.IncInvalidRequests()
		var maxBytesErr *http.MaxBytesError
		switch {
		case errors.Is(err, errUnsupportedContentType):
			http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
		case errors.As(err, &maxBytesErr):
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		default:
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}

	ingestCtx := r.Context()
	if h.requestTimeout > 0 {
		var cancel context.CancelFunc
		ingestCtx, cancel = context.WithTimeout(r.Context(), h.requestTimeout)
		defer cancel()
	}

	if err := h.ingestion.ProcessBatch(ingestCtx, records); err != nil {
		logTransient := false
		switch {
		case errors.Is(err, service.ErrQueueFull):
			logTransient = true
			w.Header().Set("Retry-After", "1")
			http.Error(w, "queue full", http.StatusTooManyRequests)
		case errors.Is(err, service.ErrProducerNotStarted),
			errors.Is(err, service.ErrProducerStopped),
			errors.Is(err, service.ErrProducerCircuitOpen),
			errors.Is(err, service.ErrIngestionStopped),
			errors.Is(err, service.ErrProducerNotConfigured),
			errors.Is(err, service.ErrLogStoreNotConfigured),
			errors.Is(err, context.DeadlineExceeded),
			errors.Is(err, context.Canceled):
			logTransient = true
			w.Header().Set("Retry-After", "1")
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		default:
			http.Error(w, "failed to process logs", http.StatusInternalServerError)
		}
		if logTransient {
			h.logTransientProcessError(err)
		} else {
			h.logger.Error("failed to process logs", loggerpkg.F("error", err))
		}
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) logTransientProcessError(err error) {
	now := time.Now().UTC().UnixNano()
	last := h.lastTransientLogNs.Load()
	if now-last < int64(transientLogInterval) {
		return
	}
	if h.lastTransientLogNs.CompareAndSwap(last, now) {
		h.logger.Warn("transient /logs processing issue", loggerpkg.F("error", err))
	}
}

func (h *Handler) decodeRecords(r *http.Request) ([]model.LogRecord, error) {
	contentType := r.Header.Get("Content-Type")
	switch {
	case strings.Contains(contentType, "application/json"):
		return h.decodeJSON(r.Body)
	case strings.Contains(contentType, "text/csv"):
		return h.decodeCSV(r.Body)
	default:
		return nil, fmt.Errorf("%w: %s", errUnsupportedContentType, contentType)
	}
}

func (h *Handler) decodeJSON(body io.Reader) ([]model.LogRecord, error) {
	decoder := json.NewDecoder(body)
	var records []model.LogRecord
	if err := decoder.Decode(&records); err != nil {
		return nil, fmt.Errorf("invalid JSON payload: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("invalid JSON payload: unexpected trailing data")
	}
	return h.validate(records)
}

func (h *Handler) decodeCSV(body io.Reader) ([]model.LogRecord, error) {
	reader := csv.NewReader(body)
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid CSV payload: %w", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("empty CSV payload")
	}

	startIdx := 0
	header := rows[0]
	if len(header) >= 3 && strings.EqualFold(header[0], "timestamp") {
		startIdx = 1
	}

	var records []model.LogRecord
	for i := startIdx; i < len(rows); i++ {
		row := rows[i]
		if len(row) < 3 {
			return nil, fmt.Errorf("row %d missing required columns", i+1)
		}
		ts, err := time.Parse(time.RFC3339, strings.TrimSpace(row[0]))
		if err != nil {
			return nil, fmt.Errorf("row %d has invalid timestamp: %w", i+1, err)
		}
		rec := model.LogRecord{
			Timestamp: ts,
			Path:      strings.TrimSpace(row[1]),
			UserAgent: strings.TrimSpace(row[2]),
		}
		records = append(records, rec)
	}
	return h.validate(records)
}

func (h *Handler) validate(records []model.LogRecord) ([]model.LogRecord, error) {
	if len(records) == 0 {
		return nil, errors.New("no log records provided")
	}
	for i := range records {
		rec := records[i]
		if rec.Timestamp.IsZero() {
			return nil, fmt.Errorf("record %d missing timestamp", i)
		}
		if strings.TrimSpace(rec.Path) == "" {
			return nil, fmt.Errorf("record %d missing path", i)
		}
		if strings.TrimSpace(rec.UserAgent) == "" {
			return nil, fmt.Errorf("record %d missing userAgent", i)
		}
		// normalize values for downstream storage.
		records[i].Timestamp = rec.Timestamp.UTC()
		records[i].Path = strings.TrimSpace(rec.Path)
		records[i].UserAgent = strings.TrimSpace(rec.UserAgent)
	}
	return records, nil
}
