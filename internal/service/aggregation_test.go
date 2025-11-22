package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/util"
)

func TestAggregationService(t *testing.T) {
	cases := []struct {
		name        string
		seed        func(t *testing.T, logsDir string)
		run         func(ctx context.Context, agg *AggregationService) error
		verify      func(t *testing.T, analyticsDir string)
		targetHour  time.Time
		analyticsAt func(logsDir string) string
	}{
		{
			name:       "aggregate single hour",
			targetHour: time.Date(2025, 11, 15, 14, 30, 0, 0, time.UTC),
			seed: func(t *testing.T, logsDir string) {
				hour := time.Date(2025, 11, 15, 14, 30, 0, 0, time.UTC)
				dateDir := filepath.Join(logsDir, hour.Format(util.DateLayout))
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
			},
			run: func(ctx context.Context, agg *AggregationService) error {
				return agg.AggregateHour(ctx, time.Date(2025, 11, 15, 14, 30, 0, 0, time.UTC))
			},
			verify: func(t *testing.T, analyticsDir string) {
				summaryPath := filepath.Join(analyticsDir, "2025-11-15", "summary_14.json")
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
			},
		},
		{
			name: "aggregate all hours",
			seed: func(t *testing.T, logsDir string) {
				seed := func(ts time.Time, lines []string) {
					dateDir := filepath.Join(logsDir, ts.Format(util.DateLayout))
					if err := os.MkdirAll(dateDir, 0o755); err != nil {
						t.Fatalf("failed to create log dir: %v", err)
					}
					logPath := filepath.Join(dateDir, fmt.Sprintf("%s.log.json", ts.Format(util.HourLayout)))
					payload := strings.Join(lines, "\n") + "\n"
					if err := os.WriteFile(logPath, []byte(payload), 0o644); err != nil {
						t.Fatalf("failed to seed log file: %v", err)
					}
				}

				hour14 := time.Date(2025, 11, 15, 14, 0, 0, 0, time.UTC)
				seed(hour14, []string{
					`{"timestamp":"2025-11-15T14:00:00Z","path":"/home","userAgent":"ua1"}`,
					`{"timestamp":"2025-11-15T14:10:00Z","path":"/home","userAgent":"ua1"}`,
				})
				hour15 := time.Date(2025, 11, 15, 15, 0, 0, 0, time.UTC)
				seed(hour15, []string{
					`{"timestamp":"2025-11-15T15:05:00Z","path":"/login","userAgent":"ua2"}`,
				})
			},
			run: func(ctx context.Context, agg *AggregationService) error {
				return agg.AggregateAll(ctx)
			},
			verify: func(t *testing.T, analyticsDir string) {
				summary14Path := filepath.Join(analyticsDir, "2025-11-15", "summary_14.json")
				if _, err := os.Stat(summary14Path); err != nil {
					t.Fatalf("expected summary for hour14, got error: %v", err)
				}
				summary15Path := filepath.Join(analyticsDir, "2025-11-15", "summary_15.json")
				if _, err := os.Stat(summary15Path); err != nil {
					t.Fatalf("expected summary for hour15, got error: %v", err)
				}

				data, err := os.ReadFile(summary15Path)
				if err != nil {
					t.Fatalf("failed to read summary: %v", err)
				}
				var summary struct {
					RequestsPerPath map[string]int `json:"requestsPerPath"`
				}
				if err := json.Unmarshal(data, &summary); err != nil {
					t.Fatalf("failed to unmarshal summary: %v", err)
				}
				if summary.RequestsPerPath["/login"] != 1 {
					t.Fatalf("unexpected path count for /login: %+v", summary.RequestsPerPath)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			logsDir := t.TempDir()
			analyticsDir := filepath.Join(logsDir, "analytics")

			tc.seed(t, logsDir)
			agg := NewAggregationService(logsDir, analyticsDir, time.Minute, loggerpkg.NewNop())
			if err := tc.run(context.Background(), agg); err != nil {
				t.Fatalf("aggregation run returned error: %v", err)
			}
			tc.verify(t, analyticsDir)
		})
	}
}
