package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/example/logpipeline/internal/domain"
	"github.com/example/logpipeline/internal/metrics"
)

// AggregationService periodically summarizes ingested logs.
type AggregationService struct {
	logsDir      string
	analyticsDir string
	interval     time.Duration
	logger       *log.Logger
}

// NewAggregationService builds a new aggregator instance.
func NewAggregationService(logsDir, analyticsDir string, interval time.Duration, logger *log.Logger) *AggregationService {
	if logger == nil {
		logger = log.Default()
	}
	return &AggregationService{
		logsDir:      logsDir,
		analyticsDir: analyticsDir,
		interval:     interval,
		logger:       logger,
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
	if err := a.AggregateCurrentHour(ctx); err != nil {
		a.logger.Printf("aggregation run failed: %v", err)
	}
	metrics.IncAggregationRuns()
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
	dateDir := hourStart.Format("2006-01-02")
	hourFile := fmt.Sprintf("%s.log.json", hourStart.Format("15"))
	filePath := filepath.Join(a.logsDir, dateDir, hourFile)

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
			a.logger.Printf("skip malformed log record: %v", err)
			continue
		}
		requestsPerPath[rec.Path]++
		requestsPerUserAgent[rec.UserAgent]++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan log file: %w", err)
	}

	summary := map[string]any{
		"hour":                 hourStart.Format(time.RFC3339),
		"requestsPerPath":      requestsPerPath,
		"requestsPerUserAgent": requestsPerUserAgent,
	}

	if err := os.MkdirAll(a.analyticsDir, 0o755); err != nil {
		return fmt.Errorf("create analytics directory: %w", err)
	}

	summaryPath := filepath.Join(a.analyticsDir, fmt.Sprintf("summary_%s.json", hourStart.Format("15")))
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal summary: %w", err)
	}
	if err := os.WriteFile(summaryPath, data, 0o644); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}

	return nil
}
