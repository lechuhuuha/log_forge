package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lechuhuuha/log_forge/internal/domain"
	"github.com/lechuhuuha/log_forge/internal/metrics"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/util"
)

// AggregationService periodically summarizes ingested logs.
type AggregationService struct {
	logsDir      string
	analyticsDir string
	interval     time.Duration
	logger       loggerpkg.Logger
}

// NewAggregationService builds a new aggregator instance.
func NewAggregationService(logsDir, analyticsDir string, interval time.Duration, logr loggerpkg.Logger) *AggregationService {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	return &AggregationService{
		logsDir:      logsDir,
		analyticsDir: analyticsDir,
		interval:     interval,
		logger:       logr,
	}
}

// Start launches the periodic aggregation loop until the context is cancelled.
func (a *AggregationService) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(a.interval)
		defer ticker.Stop()

		// Run immediately once at startup.
		a.runOnce(ctx)

		for {
			select {
			case <-ticker.C:
				a.runOnce(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (a *AggregationService) runOnce(ctx context.Context) {
	if err := a.AggregateAll(ctx); err != nil {
		a.logger.Error("aggregation run failed", loggerpkg.F("error", err))
	}
	metrics.IncAggregationRuns()
}

// AggregateAll walks the logs directory and aggregates every discovered hour file.
func (a *AggregationService) AggregateAll(ctx context.Context) error {
	return filepath.WalkDir(a.logsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			a.logger.Warn("skip path during aggregation walk", loggerpkg.F("path", path), loggerpkg.F("error", err))
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".log.json") {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		hourStart, ok := a.parseHourFromPath(path)
		if !ok {
			a.logger.Warn("skip unexpected log path", loggerpkg.F("path", path))
			return nil
		}

		if err := a.aggregateFile(ctx, path, hourStart); err != nil {
			a.logger.Error("aggregate hour failed", loggerpkg.F("path", path), loggerpkg.F("error", err))
		}
		return nil
	})
}

// AggregateCurrentHour aggregates logs for the current UTC hour.
func (a *AggregationService) AggregateCurrentHour(ctx context.Context) error {
	return a.AggregateHour(ctx, time.Now().UTC())
}

// AggregateHour aggregates logs for the hour that contains the provided time.
func (a *AggregationService) AggregateHour(ctx context.Context, ts time.Time) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	hourStart := ts.UTC().Truncate(time.Hour)
	dateDir := hourStart.Format(util.DateLayout)
	hourFile := fmt.Sprintf("%s.log.json", hourStart.Format(util.HourLayout))
	filePath := filepath.Join(a.logsDir, dateDir, hourFile)

	return a.aggregateFile(ctx, filePath, hourStart)
}

func (a *AggregationService) aggregateFile(ctx context.Context, filePath string, hourStart time.Time) error {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat log file: %w", err)
	}

	summaryPath := a.summaryPath(hourStart)
	if summaryInfo, err := os.Stat(summaryPath); err == nil {
		if !summaryInfo.ModTime().Before(fileInfo.ModTime()) {
			// summary is up-to-date with the log file
			return nil
		}
	}

	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			// No data for this hour yet; nothing to do.
			return nil
		}
		return fmt.Errorf("open log file: %w", err)
	}
	defer file.Close()

	requestsPerPath := make(map[string]int)
	requestsPerUserAgent := make(map[string]int)

	scanner := bufio.NewScanner(file)
	buf := make([]byte, 0, 1024)
	scanner.Buffer(buf, 1024*1024) // allow up to 1MB per line
	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var rec domain.LogRecord
		if err := json.Unmarshal(scanner.Bytes(), &rec); err != nil {
			a.logger.Warn("skip malformed log record", loggerpkg.F("error", err))
			continue
		}
		requestsPerPath[rec.Path]++
		requestsPerUserAgent[rec.UserAgent]++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan log file: %w", err)
	}

	return a.writeSummary(hourStart, requestsPerPath, requestsPerUserAgent)
}

func (a *AggregationService) writeSummary(hourStart time.Time, requestsPerPath, requestsPerUserAgent map[string]int) error {
	summary := map[string]any{
		"hour":                 hourStart.Format(time.RFC3339),
		"requestsPerPath":      requestsPerPath,
		"requestsPerUserAgent": requestsPerUserAgent,
	}

	dateDirAnalytics := filepath.Dir(a.summaryPath(hourStart))
	if err := os.MkdirAll(dateDirAnalytics, 0o755); err != nil {
		return fmt.Errorf("create analytics directory: %w", err)
	}

	summaryPath := a.summaryPath(hourStart)
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	if err := os.WriteFile(summaryPath, data, 0o644); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}

	return nil
}

func (a *AggregationService) summaryPath(hourStart time.Time) string {
	dateDir := hourStart.Format(util.DateLayout)
	dateDirAnalytics := filepath.Join(a.analyticsDir, dateDir)
	return filepath.Join(dateDirAnalytics, fmt.Sprintf("summary_%s.json", hourStart.Format(util.HourLayout)))
}

func (a *AggregationService) parseHourFromPath(path string) (time.Time, bool) {
	rel, err := filepath.Rel(a.logsDir, path)
	if err != nil {
		return time.Time{}, false
	}
	dateDir := filepath.Dir(rel)
	base := filepath.Base(rel)
	if dateDir == "." {
		return time.Time{}, false
	}
	if !strings.HasSuffix(base, ".log.json") {
		return time.Time{}, false
	}
	hourStr := strings.TrimSuffix(base, ".log.json")

	dateParsed, err := time.Parse(util.DateLayout, dateDir)
	if err != nil {
		return time.Time{}, false
	}
	hourParsed, err := time.Parse(util.HourLayout, hourStr)
	if err != nil {
		return time.Time{}, false
	}

	hourStart := time.Date(dateParsed.Year(), dateParsed.Month(), dateParsed.Day(), hourParsed.Hour(), 0, 0, 0, time.UTC)
	return hourStart, true
}
