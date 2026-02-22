package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/repo"
	"github.com/lechuhuuha/log_forge/util"
)

func TestParseHourFromObjectKey(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		want      time.Time
		wantValid bool
	}{
		{
			name:      "sharded key",
			key:       "logs/2026-01-01/09/pod-a-1.log.json",
			want:      time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
			wantValid: true,
		},
		{
			name:      "legacy flat key",
			key:       "logs/2026-01-01/10.log.json",
			want:      time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
			wantValid: true,
		},
		{
			name:      "invalid key",
			key:       "logs/not-a-date/file.log.json",
			wantValid: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseHourFromObjectKey(tc.key)
			if ok != tc.wantValid {
				t.Fatalf("unexpected valid flag: got=%v want=%v", ok, tc.wantValid)
			}
			if !tc.wantValid {
				return
			}
			if !got.Equal(tc.want) {
				t.Fatalf("unexpected hour: got=%s want=%s", got, tc.want)
			}
		})
	}
}

func TestMinIOAggregationServiceAggregateAll(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, store *fakeMinIOAggregationStore, analyticsDir string)
		run       func(ctx context.Context, agg *MinIOAggregationService) error
		assertion func(t *testing.T, analyticsDir string, err error)
	}{
		{
			name: "aggregates sharded objects per hour",
			setup: func(_ *testing.T, store *fakeMinIOAggregationStore, _ string) {
				store.putObject(
					"logs/2025-11-15/14/pod-a-1.log.json",
					[]byte(
						`{"timestamp":"2025-11-15T14:00:00Z","path":"/home","userAgent":"ua1"}`+"\n"+
							`{"timestamp":"2025-11-15T14:10:00Z","path":"/home","userAgent":"ua1"}`+"\n",
					),
					time.Now().Add(-2*time.Minute),
				)
				store.putObject(
					"logs/2025-11-15/14/pod-b-1.log.json",
					[]byte(`{"timestamp":"2025-11-15T14:20:00Z","path":"/login","userAgent":"ua2"}`+"\n"),
					time.Now().Add(-time.Minute),
				)
				store.putObject(
					"logs/2025-11-15/15/pod-b-2.log.json",
					[]byte(`{"timestamp":"2025-11-15T15:05:00Z","path":"/contact","userAgent":"ua2"}`+"\n"),
					time.Now(),
				)
			},
			run: func(ctx context.Context, agg *MinIOAggregationService) error {
				return agg.AggregateAll(ctx)
			},
			assertion: func(t *testing.T, analyticsDir string, err error) {
				if err != nil {
					t.Fatalf("AggregateAll returned error: %v", err)
				}
				summary14Path := filepath.Join(analyticsDir, "2025-11-15", "summary_14.json")
				summary15Path := filepath.Join(analyticsDir, "2025-11-15", "summary_15.json")
				summary14 := readSummary(t, summary14Path)
				summary15 := readSummary(t, summary15Path)
				if summary14.RequestsPerPath["/home"] != 2 || summary14.RequestsPerPath["/login"] != 1 {
					t.Fatalf("unexpected summary14 path counts: %+v", summary14.RequestsPerPath)
				}
				if summary14.RequestsPerUserAgent["ua1"] != 2 || summary14.RequestsPerUserAgent["ua2"] != 1 {
					t.Fatalf("unexpected summary14 userAgent counts: %+v", summary14.RequestsPerUserAgent)
				}
				if summary15.RequestsPerPath["/contact"] != 1 {
					t.Fatalf("unexpected summary15 path counts: %+v", summary15.RequestsPerPath)
				}
			},
		},
		{
			name: "supports legacy object layout",
			setup: func(_ *testing.T, store *fakeMinIOAggregationStore, _ string) {
				store.putObject(
					"logs/2025-11-16/08.log.json",
					[]byte(`{"timestamp":"2025-11-16T08:00:00Z","path":"/legacy","userAgent":"ua"}`+"\n"),
					time.Now(),
				)
			},
			run: func(ctx context.Context, agg *MinIOAggregationService) error {
				return agg.AggregateAll(ctx)
			},
			assertion: func(t *testing.T, analyticsDir string, err error) {
				if err != nil {
					t.Fatalf("AggregateAll returned error: %v", err)
				}
				summaryPath := filepath.Join(analyticsDir, "2025-11-16", "summary_08.json")
				summary := readSummary(t, summaryPath)
				if summary.RequestsPerPath["/legacy"] != 1 {
					t.Fatalf("unexpected legacy summary counts: %+v", summary.RequestsPerPath)
				}
			},
		},
		{
			name: "context cancelled returns error",
			setup: func(_ *testing.T, store *fakeMinIOAggregationStore, _ string) {
				store.putObject(
					"logs/2025-11-16/08/pod-a-1.log.json",
					[]byte(`{"timestamp":"2025-11-16T08:00:00Z","path":"/x","userAgent":"ua"}`+"\n"),
					time.Now(),
				)
			},
			run: func(_ context.Context, agg *MinIOAggregationService) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return agg.AggregateAll(ctx)
			},
			assertion: func(t *testing.T, _ string, err error) {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context canceled, got %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeMinIOAggregationStore()
			analyticsDir := t.TempDir()
			tc.setup(t, store, analyticsDir)
			agg := NewMinIOAggregationService(store, analyticsDir, time.Minute, nil)
			err := tc.run(context.Background(), agg)
			tc.assertion(t, analyticsDir, err)
		})
	}
}

func TestMinIOAggregationServiceStartWritesSummary(t *testing.T) {
	hour := time.Now().UTC().Truncate(time.Hour)
	store := newFakeMinIOAggregationStore()
	store.putObject(
		"logs/"+hour.Format(util.DateLayout)+"/"+hour.Format(util.HourLayout)+"/pod-a-1.log.json",
		[]byte(`{"timestamp":"`+hour.Format(time.RFC3339)+`","path":"/p","userAgent":"ua"}`+"\n"),
		time.Now(),
	)

	analyticsDir := t.TempDir()
	// Use a long interval so only the immediate startup run is expected in this test.
	agg := NewMinIOAggregationService(store, analyticsDir, time.Hour, nil)
	ctx, cancel := context.WithCancel(context.Background())
	agg.Start(ctx)

	summaryPath := filepath.Join(analyticsDir, hour.Format(util.DateLayout), "summary_"+hour.Format(util.HourLayout)+".json")
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		if _, err := os.Stat(summaryPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("summary file was not written by Start loop: %s", summaryPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	time.Sleep(10 * time.Millisecond)
}

type summaryPayload struct {
	RequestsPerPath      map[string]int `json:"requestsPerPath"`
	RequestsPerUserAgent map[string]int `json:"requestsPerUserAgent"`
}

func readSummary(t *testing.T, path string) summaryPayload {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read summary %s: %v", path, err)
	}
	var summary summaryPayload
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("decode summary %s: %v", path, err)
	}
	return summary
}

type fakeMinIOAggregationStore struct {
	mu      sync.Mutex
	objects map[string]fakeMinIOAggregationObject
	readErr map[string]error
	listErr error
}

type fakeMinIOAggregationObject struct {
	payload      []byte
	lastModified time.Time
}

func newFakeMinIOAggregationStore() *fakeMinIOAggregationStore {
	return &fakeMinIOAggregationStore{
		objects: make(map[string]fakeMinIOAggregationObject),
		readErr: make(map[string]error),
	}
}

func (s *fakeMinIOAggregationStore) ListLogObjects(ctx context.Context) ([]repo.MinIOObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}
	objects := make([]repo.MinIOObjectInfo, 0, len(s.objects))
	for key, object := range s.objects {
		objects = append(objects, repo.MinIOObjectInfo{Key: key, LastModified: object.lastModified})
	}
	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})
	return objects, nil
}

func (s *fakeMinIOAggregationStore) ReadLogObject(ctx context.Context, object string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.readErr[object]; ok {
		return nil, err
	}
	stored, ok := s.objects[object]
	if !ok {
		return nil, errors.New("object not found")
	}
	return append([]byte(nil), stored.payload...), nil
}

func (s *fakeMinIOAggregationStore) putObject(key string, payload []byte, modified time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[strings.TrimPrefix(key, "/")] = fakeMinIOAggregationObject{
		payload:      append([]byte(nil), payload...),
		lastModified: modified.UTC(),
	}
}
