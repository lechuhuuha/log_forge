package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/model"
)

func TestNewMinIORepo(t *testing.T) {
	cases := []struct {
		name      string
		opts      MinIORepoOptions
		wantErr   bool
		errorText string
	}{
		{
			name: "requires bucket",
			opts: MinIORepoOptions{
				Endpoint:  "127.0.0.1:9000",
				AccessKey: "a",
				SecretKey: "b",
			},
			wantErr:   true,
			errorText: "minio bucket is required",
		},
		{
			name: "requires endpoint when real client is used",
			opts: MinIORepoOptions{
				Bucket:    "logs",
				AccessKey: "a",
				SecretKey: "b",
			},
			wantErr:   true,
			errorText: "minio endpoint is required",
		},
		{
			name: "uses default logs prefix",
			opts: MinIORepoOptions{
				Bucket: "logs",
				client: newFakeMinIOStore(),
			},
		},
		{
			name: "trims custom logs prefix",
			opts: MinIORepoOptions{
				Bucket:     "logs",
				LogsPrefix: " /custom-prefix/ ",
				client:     newFakeMinIOStore(),
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			repo, err := NewMinIORepo(tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tc.errorText != "" && !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("expected error containing %q, got %v", tc.errorText, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewMinIORepo returned error: %v", err)
			}
			if tc.opts.LogsPrefix == " /custom-prefix/ " && repo.logsPrefix != "custom-prefix" {
				t.Fatalf("expected trimmed logs prefix custom-prefix, got %q", repo.logsPrefix)
			}
			if tc.opts.LogsPrefix == "" && repo.logsPrefix != "logs" {
				t.Fatalf("expected default logs prefix logs, got %q", repo.logsPrefix)
			}
		})
	}
}

func TestMinIORepoSaveBatch(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, store *fakeMinIOStore) (context.Context, *MinIORepo, []model.LogRecord)
		assertion func(t *testing.T, store *fakeMinIOStore, err error)
	}{
		{
			name: "groups records by utc date and hour object key",
			setup: func(t *testing.T, store *fakeMinIOStore) (context.Context, *MinIORepo, []model.LogRecord) {
				store.bucketExists = true
				r, err := NewMinIORepo(MinIORepoOptions{
					Bucket:     "logs-bucket",
					LogsPrefix: "logs",
					client:     store,
				})
				if err != nil {
					t.Fatalf("NewMinIORepo returned error: %v", err)
				}
				return context.Background(), r, []model.LogRecord{
					{
						Timestamp: time.Date(2025, 1, 2, 10, 15, 0, 0, time.FixedZone("UTC+7", 7*3600)),
						Path:      "/home",
						UserAgent: "ua1",
					},
					{
						Timestamp: time.Date(2025, 1, 2, 3, 30, 0, 0, time.UTC),
						Path:      "/about",
						UserAgent: "ua2",
					},
					{
						Timestamp: time.Date(2025, 1, 2, 4, 45, 0, 0, time.UTC),
						Path:      "/contact",
						UserAgent: "ua3",
					},
				}
			},
			assertion: func(t *testing.T, store *fakeMinIOStore, err error) {
				if err != nil {
					t.Fatalf("SaveBatch returned error: %v", err)
				}
				records03 := decodeStoredRecords(t, store, "logs-bucket", "logs/2025-01-02/03.log.json")
				records04 := decodeStoredRecords(t, store, "logs-bucket", "logs/2025-01-02/04.log.json")
				if len(records03) != 2 {
					t.Fatalf("expected 2 records in hour 03 object, got %d", len(records03))
				}
				if len(records04) != 1 {
					t.Fatalf("expected 1 record in hour 04 object, got %d", len(records04))
				}
				for _, rec := range append(records03, records04...) {
					if rec.Timestamp.Location() != time.UTC {
						t.Fatalf("expected UTC timestamp, got %v", rec.Timestamp.Location())
					}
				}
			},
		},
		{
			name: "appends to existing object",
			setup: func(t *testing.T, store *fakeMinIOStore) (context.Context, *MinIORepo, []model.LogRecord) {
				existing := model.LogRecord{
					Timestamp: time.Date(2025, 1, 2, 3, 0, 0, 0, time.UTC),
					Path:      "/existing",
					UserAgent: "ua-existing",
				}
				data, err := json.Marshal(existing)
				if err != nil {
					t.Fatalf("marshal existing record: %v", err)
				}
				store.putRaw("logs-bucket", "logs/2025-01-02/03.log.json", append(data, '\n'))

				r, err := NewMinIORepo(MinIORepoOptions{
					Bucket: "logs-bucket",
					client: store,
				})
				if err != nil {
					t.Fatalf("NewMinIORepo returned error: %v", err)
				}
				return context.Background(), r, []model.LogRecord{
					{
						Timestamp: time.Date(2025, 1, 2, 3, 30, 0, 0, time.UTC),
						Path:      "/new",
						UserAgent: "ua-new",
					},
				}
			},
			assertion: func(t *testing.T, store *fakeMinIOStore, err error) {
				if err != nil {
					t.Fatalf("SaveBatch returned error: %v", err)
				}
				records := decodeStoredRecords(t, store, "logs-bucket", "logs/2025-01-02/03.log.json")
				if len(records) != 2 {
					t.Fatalf("expected 2 records after append, got %d", len(records))
				}
				if records[0].Path != "/existing" {
					t.Fatalf("expected first record to remain existing data, got %q", records[0].Path)
				}
				if records[1].Path != "/new" {
					t.Fatalf("expected second record to be appended data, got %q", records[1].Path)
				}
			},
		},
		{
			name: "cancelled context returns error",
			setup: func(t *testing.T, store *fakeMinIOStore) (context.Context, *MinIORepo, []model.LogRecord) {
				r, err := NewMinIORepo(MinIORepoOptions{
					Bucket: "logs-bucket",
					client: store,
				})
				if err != nil {
					t.Fatalf("NewMinIORepo returned error: %v", err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, r, []model.LogRecord{
					{
						Timestamp: time.Date(2025, 1, 2, 3, 30, 0, 0, time.UTC),
						Path:      "/new",
						UserAgent: "ua-new",
					},
				}
			},
			assertion: func(t *testing.T, store *fakeMinIOStore, err error) {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected context canceled error, got %v", err)
				}
				if store.writeCount() != 0 {
					t.Fatalf("expected no writes, got %d", store.writeCount())
				}
			},
		},
		{
			name: "empty batch is no-op",
			setup: func(t *testing.T, store *fakeMinIOStore) (context.Context, *MinIORepo, []model.LogRecord) {
				r, err := NewMinIORepo(MinIORepoOptions{
					Bucket: "logs-bucket",
					client: store,
				})
				if err != nil {
					t.Fatalf("NewMinIORepo returned error: %v", err)
				}
				return context.Background(), r, nil
			},
			assertion: func(t *testing.T, store *fakeMinIOStore, err error) {
				if err != nil {
					t.Fatalf("SaveBatch returned error for empty batch: %v", err)
				}
				if store.writeCount() != 0 {
					t.Fatalf("expected no writes for empty batch, got %d", store.writeCount())
				}
			},
		},
		{
			name: "write honors context timeout",
			setup: func(t *testing.T, store *fakeMinIOStore) (context.Context, *MinIORepo, []model.LogRecord) {
				store.writeDelay = 40 * time.Millisecond
				r, err := NewMinIORepo(MinIORepoOptions{
					Bucket: "logs-bucket",
					client: store,
				})
				if err != nil {
					t.Fatalf("NewMinIORepo returned error: %v", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				t.Cleanup(cancel)
				return ctx, r, []model.LogRecord{
					{
						Timestamp: time.Date(2025, 1, 2, 3, 30, 0, 0, time.UTC),
						Path:      "/new",
						UserAgent: "ua-new",
					},
				}
			},
			assertion: func(t *testing.T, _ *fakeMinIOStore, err error) {
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("expected context deadline exceeded, got %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeMinIOStore()
			ctx, r, records := tc.setup(t, store)
			err := r.SaveBatch(ctx, records)
			tc.assertion(t, store, err)
		})
	}
}

func TestMinIORepoCheckReady(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(store *fakeMinIOStore)
		wantErr   bool
		errorText string
	}{
		{
			name: "bucket exists",
			setup: func(store *fakeMinIOStore) {
				store.bucketExists = true
			},
		},
		{
			name: "bucket missing",
			setup: func(store *fakeMinIOStore) {
				store.bucketExists = false
			},
			wantErr:   true,
			errorText: "does not exist",
		},
		{
			name: "bucket check error",
			setup: func(store *fakeMinIOStore) {
				store.bucketErr = errors.New("connection failed")
			},
			wantErr:   true,
			errorText: "check minio bucket",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeMinIOStore()
			tc.setup(store)
			r, err := NewMinIORepo(MinIORepoOptions{
				Bucket: "logs-bucket",
				client: store,
			})
			if err != nil {
				t.Fatalf("NewMinIORepo returned error: %v", err)
			}

			err = r.CheckReady(context.Background())
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected readiness error")
				}
				if tc.errorText != "" && !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("expected error containing %q, got %v", tc.errorText, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("CheckReady returned error: %v", err)
			}
		})
	}
}

type fakeMinIOStore struct {
	mu sync.Mutex

	objects  map[string][]byte
	readErr  map[string]error
	writeErr map[string]error

	bucketExists bool
	bucketErr    error
	writeDelay   time.Duration
	writeCalls   int
}

func newFakeMinIOStore() *fakeMinIOStore {
	return &fakeMinIOStore{
		objects:  make(map[string][]byte),
		readErr:  make(map[string]error),
		writeErr: make(map[string]error),
	}
}

func (s *fakeMinIOStore) BucketExists(_ context.Context, _ string) (bool, error) {
	if s.bucketErr != nil {
		return false, s.bucketErr
	}
	return s.bucketExists, nil
}

func (s *fakeMinIOStore) ReadObject(ctx context.Context, bucket, object string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s/%s", bucket, object)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err, ok := s.readErr[key]; ok {
		return nil, err
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, errObjectNotFound
	}
	copyData := append([]byte(nil), data...)
	return copyData, nil
}

func (s *fakeMinIOStore) WriteObject(ctx context.Context, bucket, object string, payload []byte) error {
	if s.writeDelay > 0 {
		timer := time.NewTimer(s.writeDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	key := fmt.Sprintf("%s/%s", bucket, object)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeCalls++
	if err, ok := s.writeErr[key]; ok {
		return err
	}
	s.objects[key] = append([]byte(nil), payload...)
	return nil
}

func (s *fakeMinIOStore) putRaw(bucket, object string, payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s/%s", bucket, object)
	s.objects[key] = append([]byte(nil), payload...)
}

func (s *fakeMinIOStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeCalls
}

func decodeStoredRecords(t *testing.T, store *fakeMinIOStore, bucket, object string) []model.LogRecord {
	t.Helper()
	data, err := store.ReadObject(context.Background(), bucket, object)
	if err != nil {
		t.Fatalf("read stored object %s/%s: %v", bucket, object, err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	records := make([]model.LogRecord, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec model.LogRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("unmarshal object line: %v", err)
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.Before(records[j].Timestamp)
	})
	return records
}
