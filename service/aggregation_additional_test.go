package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/util"
)

func TestAggregationServiceAdditionalFlows(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Hour)

	cases := []struct {
		name string
		run  func(t *testing.T, logsDir, analyticsDir string, hour time.Time)
	}{
		{
			name: "aggregate current hour",
			run: func(t *testing.T, logsDir, analyticsDir string, hour time.Time) {
				seedHourFile(t, logsDir, hour, []string{
					`{"timestamp":"` + hour.Format(time.RFC3339) + `","path":"/p","userAgent":"ua"}`,
				})

				agg := NewAggregationService(logsDir, analyticsDir, time.Minute, nil)
				if err := agg.AggregateCurrentHour(context.Background()); err != nil {
					t.Fatalf("AggregateCurrentHour returned error: %v", err)
				}

				summary := filepath.Join(analyticsDir, hour.Format(util.DateLayout), "summary_"+hour.Format(util.HourLayout)+".json")
				if _, err := os.Stat(summary); err != nil {
					t.Fatalf("expected summary file %s: %v", summary, err)
				}
			},
		},
		{
			name: "run and start loops produce summary",
			run: func(t *testing.T, logsDir, analyticsDir string, hour time.Time) {
				seedHourFile(t, logsDir, hour, []string{
					`{"timestamp":"` + hour.Format(time.RFC3339) + `","path":"/x","userAgent":"ua"}`,
				})

				agg := NewAggregationService(logsDir, analyticsDir, 10*time.Millisecond, nil)

				// Cover direct run path.
				agg.run(context.Background())

				// Cover ticker-based Start path.
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				agg.Start(ctx)

				summary := filepath.Join(analyticsDir, hour.Format(util.DateLayout), "summary_"+hour.Format(util.HourLayout)+".json")
				deadline := time.Now().Add(300 * time.Millisecond)
				for {
					if _, err := os.Stat(summary); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("summary file was not written by Start loop: %s", summary)
					}
					time.Sleep(10 * time.Millisecond)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			logsDir := t.TempDir()
			analyticsDir := t.TempDir()
			tc.run(t, logsDir, analyticsDir, now)
		})
	}
}

func seedHourFile(t *testing.T, logsDir string, ts time.Time, records []string) {
	t.Helper()
	dateDir := filepath.Join(logsDir, ts.Format(util.DateLayout))
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dateDir, ts.Format(util.HourLayout)+".log.json")
	content := ""
	for _, rec := range records {
		content += rec + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write log file: %v", err)
	}
}
