package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lechuhuuha/log_forge/internal/metrics"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/model"
	"github.com/lechuhuuha/log_forge/repo"
	"github.com/lechuhuuha/log_forge/util"
)

// MinIOAggregationStore reads log object payloads for aggregation.
type MinIOAggregationStore interface {
	ListLogObjects(ctx context.Context) ([]repo.MinIOObjectInfo, error)
	ReadLogObject(ctx context.Context, object string) ([]byte, error)
}

// MinIOAggregationService periodically summarizes MinIO-backed logs.
type MinIOAggregationService struct {
	store        MinIOAggregationStore
	analyticsDir string
	interval     time.Duration
	logger       loggerpkg.Logger
}

// NewMinIOAggregationService builds a MinIO-backed aggregator instance.
func NewMinIOAggregationService(store MinIOAggregationStore, analyticsDir string, interval time.Duration, logr loggerpkg.Logger) *MinIOAggregationService {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	return &MinIOAggregationService{
		store:        store,
		analyticsDir: analyticsDir,
		interval:     interval,
		logger:       logr,
	}
}

// Start launches the periodic aggregation loop until the context is cancelled.
func (a *MinIOAggregationService) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(a.interval)
		defer ticker.Stop()

		// Run immediately once at startup.
		a.run(ctx)

		for {
			select {
			case <-ticker.C:
				a.run(ctx)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (a *MinIOAggregationService) run(ctx context.Context) {
	if err := a.AggregateAll(ctx); err != nil {
		a.logger.Error("minio aggregation run failed", loggerpkg.F("error", err))
	}
	metrics.IncAggregationRuns()
}

// AggregateAll groups MinIO log objects by hour and writes summary files.
func (a *MinIOAggregationService) AggregateAll(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	objects, err := a.store.ListLogObjects(ctx)
	if err != nil {
		return fmt.Errorf("list minio log objects: %w", err)
	}

	grouped := make(map[time.Time][]repo.MinIOObjectInfo)
	for _, object := range objects {
		if err := ctx.Err(); err != nil {
			return err
		}
		hourStart, ok := parseHourFromObjectKey(object.Key)
		if !ok {
			a.logger.Warn("skip unexpected minio object path", loggerpkg.F("object", object.Key))
			continue
		}
		grouped[hourStart] = append(grouped[hourStart], object)
	}

	hours := make([]time.Time, 0, len(grouped))
	for hour := range grouped {
		hours = append(hours, hour)
	}
	sort.Slice(hours, func(i, j int) bool {
		return hours[i].Before(hours[j])
	})

	for _, hour := range hours {
		if err := a.aggregateHourObjects(ctx, hour, grouped[hour]); err != nil {
			a.logger.Error("aggregate minio hour failed", loggerpkg.F("hour", hour), loggerpkg.F("error", err))
		}
	}
	return nil
}

func parseHourFromObjectKey(objectKey string) (time.Time, bool) {
	objectKey = strings.Trim(strings.TrimSpace(objectKey), "/")
	if objectKey == "" {
		return time.Time{}, false
	}
	parts := strings.Split(objectKey, "/")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	fileName := parts[len(parts)-1]
	if !strings.HasSuffix(fileName, ".log.json") {
		return time.Time{}, false
	}

	// Sharded path: .../<date>/<hour>/<writer-seq>.log.json
	if len(parts) >= 3 {
		if hourStart, ok := parseHourParts(parts[len(parts)-3], parts[len(parts)-2]); ok {
			return hourStart, true
		}
	}

	// Legacy path: .../<date>/<hour>.log.json
	hourToken := strings.TrimSuffix(fileName, ".log.json")
	if hourStart, ok := parseHourParts(parts[len(parts)-2], hourToken); ok {
		return hourStart, true
	}
	return time.Time{}, false
}

func parseHourParts(dateToken, hourToken string) (time.Time, bool) {
	dateParsed, err := time.Parse(util.DateLayout, dateToken)
	if err != nil {
		return time.Time{}, false
	}
	hourParsed, err := time.Parse(util.HourLayout, hourToken)
	if err != nil {
		return time.Time{}, false
	}
	hourStart := time.Date(dateParsed.Year(), dateParsed.Month(), dateParsed.Day(), hourParsed.Hour(), 0, 0, 0, time.UTC)
	return hourStart, true
}

func (a *MinIOAggregationService) aggregateHourObjects(ctx context.Context, hourStart time.Time, objects []repo.MinIOObjectInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(objects) == 0 {
		return nil
	}

	latestObjectModTime := objects[0].LastModified
	for _, object := range objects[1:] {
		if object.LastModified.After(latestObjectModTime) {
			latestObjectModTime = object.LastModified
		}
	}

	summaryPath := a.summaryPath(hourStart)
	if summaryInfo, err := os.Stat(summaryPath); err == nil {
		if !summaryInfo.ModTime().Before(latestObjectModTime) {
			return nil
		}
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})

	requestsPerPath := make(map[string]int)
	requestsPerUserAgent := make(map[string]int)

	for _, object := range objects {
		if err := ctx.Err(); err != nil {
			return err
		}
		payload, err := a.store.ReadLogObject(ctx, object.Key)
		if err != nil {
			return fmt.Errorf("read minio object %q: %w", object.Key, err)
		}
		if err := aggregateNDJSONPayload(ctx, payload, requestsPerPath, requestsPerUserAgent, a.logger); err != nil {
			return fmt.Errorf("aggregate payload for object %q: %w", object.Key, err)
		}
	}

	return a.writeSummary(hourStart, requestsPerPath, requestsPerUserAgent)
}

func aggregateNDJSONPayload(
	ctx context.Context,
	payload []byte,
	requestsPerPath map[string]int,
	requestsPerUserAgent map[string]int,
	logr loggerpkg.Logger,
) error {
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	buf := make([]byte, 0, 1024)
	scanner.Buffer(buf, 1024*1024) // allow up to 1MB per line
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec model.LogRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			logr.Warn("skip malformed minio log record", loggerpkg.F("error", err))
			continue
		}
		requestsPerPath[rec.Path]++
		requestsPerUserAgent[rec.UserAgent]++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

func (a *MinIOAggregationService) writeSummary(hourStart time.Time, requestsPerPath, requestsPerUserAgent map[string]int) error {
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

func (a *MinIOAggregationService) summaryPath(hourStart time.Time) string {
	dateDir := hourStart.Format(util.DateLayout)
	dateDirAnalytics := filepath.Join(a.analyticsDir, dateDir)
	return filepath.Join(dateDirAnalytics, fmt.Sprintf("summary_%s.json", hourStart.Format(util.HourLayout)))
}
