package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

func TestAggregationServiceAggregateHour(t *testing.T) {
	logsDir := t.TempDir()
	analyticsDir := filepath.Join(logsDir, "analytics")

	hour := time.Date(2025, 11, 15, 14, 30, 0, 0, time.UTC)
	dateDir := filepath.Join(logsDir, hour.Format("2006-01-02"))
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatalf("failed to create log dir: %v", err)
	}
	logPath := filepath.Join(dateDir, "14.log.json")
	payload := "" +
		`{"timestamp":"2025-11-15T14:00:00Z","path":"/home","userAgent":"ua1"}` + "\n" +
		`{"timestamp":"2025-11-15T14:10:00Z","path":"/home","userAgent":"ua1"}` + "\n" +
		`{"timestamp":"2025-11-15T14:15:00Z","path":"/login","userAgent":"ua2"}` + "\n"
	if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
		t.Fatalf("failed to seed log file: %v", err)
	}

	agg := NewAggregationService(logsDir, analyticsDir, time.Minute, loggerpkg.NewNop())
	if err := agg.AggregateHour(context.Background(), hour); err != nil {
		t.Fatalf("AggregateHour returned error: %v", err)
	}

	summaryPath := filepath.Join(analyticsDir, "summary_14.json")
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("expected summary file, got error: %v", err)
	}

	var summary struct {
		RequestsPerPath      map[string]int `json:"requestsPerPath"`
		RequestsPerUserAgent map[string]int `json:"requestsPerUserAgent"`
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("failed to unmarshal summary: %v", err)
	}

	if summary.RequestsPerPath["/home"] != 2 || summary.RequestsPerPath["/login"] != 1 {
		t.Fatalf("unexpected path counts: %+v", summary.RequestsPerPath)
	}
	if summary.RequestsPerUserAgent["ua1"] != 2 || summary.RequestsPerUserAgent["ua2"] != 1 {
		t.Fatalf("unexpected user agent counts: %+v", summary.RequestsPerUserAgent)
	}
}
