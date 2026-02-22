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
		check     func(t *testing.T, repo *MinIORepo)
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
			check: func(t *testing.T, repo *MinIORepo) {
				if repo.logsPrefix != "logs" {
					t.Fatalf("expected default logs prefix logs, got %q", repo.logsPrefix)
				}
				if strings.TrimSpace(repo.writerID) == "" {
					t.Fatal("expected non-empty generated writer id")
				}
			},
		},
		{
			name: "trims custom logs prefix",
			opts: MinIORepoOptions{
				Bucket:     "logs",
				LogsPrefix: " /custom-prefix/ ",
				WriterID:   "pod-A",
				client:     newFakeMinIOStore(),
			},
			check: func(t *testing.T, repo *MinIORepo) {
				if repo.logsPrefix != "custom-prefix" {
					t.Fatalf("expected trimmed logs prefix custom-prefix, got %q", repo.logsPrefix)
				}
				if !strings.HasPrefix(repo.writerID, "pod-A-") {
					t.Fatalf("expected writer id to use provided base, got %q", repo.writerID)
				}
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
			if tc.check != nil {
				tc.check(t, repo)
			}
		})
	}
}

func TestMinIORepoSaveBatchGroupsByUTCHourShards(t *testing.T) {
	store := newFakeMinIOStore()
	r, err := NewMinIORepo(MinIORepoOptions{
		Bucket:     "logs-bucket",
		LogsPrefix: "logs",
		WriterID:   "pod-a",
		client:     store,
	})
	if err != nil {
		t.Fatalf("NewMinIORepo returned error: %v", err)
	}

	err = r.SaveBatch(context.Background(), []model.LogRecord{
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
	})
	if err != nil {
		t.Fatalf("SaveBatch returned error: %v", err)
	}

	objects, err := r.ListLogObjects(context.Background())
	if err != nil {
		t.Fatalf("ListLogObjects returned error: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 sharded objects (one per hour in this batch), got %d", len(objects))
	}

	var (
		hour03Object string
		hour04Object string
	)
	for _, obj := range objects {
		switch {
		case strings.Contains(obj.Key, "logs/2025-01-02/03/"):
			hour03Object = obj.Key
		case strings.Contains(obj.Key, "logs/2025-01-02/04/"):
			hour04Object = obj.Key
		}
	}
	if hour03Object == "" {
		t.Fatal("expected object key under logs/2025-01-02/03/")
	}
	if hour04Object == "" {
		t.Fatal("expected object key under logs/2025-01-02/04/")
	}

	records03 := decodeStoredRecords(t, store, "logs-bucket", hour03Object)
	records04 := decodeStoredRecords(t, store, "logs-bucket", hour04Object)
	if len(records03) != 2 {
		t.Fatalf("expected 2 records in hour 03 shard, got %d", len(records03))
	}
	if len(records04) != 1 {
		t.Fatalf("expected 1 record in hour 04 shard, got %d", len(records04))
	}
	for _, rec := range append(records03, records04...) {
		if rec.Timestamp.Location() != time.UTC {
			t.Fatalf("expected UTC timestamp, got %v", rec.Timestamp.Location())
		}
	}
}

func TestMinIORepoSaveBatchAvoidsCrossWriterOverwrite(t *testing.T) {
	store := newFakeMinIOStore()
	repoA, err := NewMinIORepo(MinIORepoOptions{Bucket: "logs-bucket", LogsPrefix: "logs", WriterID: "pod-a", client: store})
	if err != nil {
		t.Fatalf("NewMinIORepo repoA returned error: %v", err)
	}
	repoB, err := NewMinIORepo(MinIORepoOptions{Bucket: "logs-bucket", LogsPrefix: "logs", WriterID: "pod-b", client: store})
	if err != nil {
		t.Fatalf("NewMinIORepo repoB returned error: %v", err)
	}

	batchA := []model.LogRecord{{
		Timestamp: time.Date(2025, 1, 2, 3, 5, 0, 0, time.UTC),
		Path:      "/from-a",
		UserAgent: "ua-a",
	}}
	batchB := []model.LogRecord{{
		Timestamp: time.Date(2025, 1, 2, 3, 6, 0, 0, time.UTC),
		Path:      "/from-b",
		UserAgent: "ua-b",
	}}

	if err := repoA.SaveBatch(context.Background(), batchA); err != nil {
		t.Fatalf("repoA SaveBatch returned error: %v", err)
	}
	if err := repoB.SaveBatch(context.Background(), batchB); err != nil {
		t.Fatalf("repoB SaveBatch returned error: %v", err)
	}

	objects, err := repoA.ListLogObjects(context.Background())
	if err != nil {
		t.Fatalf("ListLogObjects returned error: %v", err)
	}

	hourObjects := make([]string, 0)
	for _, obj := range objects {
		if strings.Contains(obj.Key, "logs/2025-01-02/03/") {
			hourObjects = append(hourObjects, obj.Key)
		}
	}
	if len(hourObjects) != 2 {
		t.Fatalf("expected 2 independent shard objects for two writers, got %d (%v)", len(hourObjects), hourObjects)
	}

	paths := map[string]struct{}{}
	for _, object := range hourObjects {
		records := decodeStoredRecords(t, store, "logs-bucket", object)
		for _, rec := range records {
			paths[rec.Path] = struct{}{}
		}
	}
	if _, ok := paths["/from-a"]; !ok {
		t.Fatal("missing record written by writer A")
	}
	if _, ok := paths["/from-b"]; !ok {
		t.Fatal("missing record written by writer B")
	}
}

func TestMinIORepoSaveBatchContextAndNoOp(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, store *fakeMinIOStore) (context.Context, *MinIORepo, []model.LogRecord)
		assertion func(t *testing.T, store *fakeMinIOStore, err error)
	}{
		{
			name: "cancelled context returns error",
			setup: func(t *testing.T, store *fakeMinIOStore) (context.Context, *MinIORepo, []model.LogRecord) {
				r, err := NewMinIORepo(MinIORepoOptions{Bucket: "logs-bucket", client: store})
				if err != nil {
					t.Fatalf("NewMinIORepo returned error: %v", err)
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, r, []model.LogRecord{{
					Timestamp: time.Date(2025, 1, 2, 3, 30, 0, 0, time.UTC),
					Path:      "/new",
					UserAgent: "ua-new",
				}}
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
				r, err := NewMinIORepo(MinIORepoOptions{Bucket: "logs-bucket", client: store})
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
				r, err := NewMinIORepo(MinIORepoOptions{Bucket: "logs-bucket", client: store})
				if err != nil {
					t.Fatalf("NewMinIORepo returned error: %v", err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				t.Cleanup(cancel)
				return ctx, r, []model.LogRecord{{
					Timestamp: time.Date(2025, 1, 2, 3, 30, 0, 0, time.UTC),
					Path:      "/new",
					UserAgent: "ua-new",
				}}
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
			ctx, repo, records := tc.setup(t, store)
			err := repo.SaveBatch(ctx, records)
			tc.assertion(t, store, err)
		})
	}
}

func TestMinIORepoListAndReadLogObjects(t *testing.T) {
	store := newFakeMinIOStore()
	store.putRaw("logs-bucket", "logs/2025-01-02/03/pod-a-1.log.json", []byte("{}\n"), time.Now().Add(-time.Minute))
	store.putRaw("logs-bucket", "logs/2025-01-02/03/not-log.txt", []byte("ignored\n"), time.Now())
	store.putRaw("logs-bucket", "logs/2025-01-02/03/pod-a-2.log.json", []byte("{}\n"), time.Now())

	repo, err := NewMinIORepo(MinIORepoOptions{Bucket: "logs-bucket", client: store})
	if err != nil {
		t.Fatalf("NewMinIORepo returned error: %v", err)
	}

	objects, err := repo.ListLogObjects(context.Background())
	if err != nil {
		t.Fatalf("ListLogObjects returned error: %v", err)
	}
	if len(objects) != 2 {
		t.Fatalf("expected 2 .log.json objects, got %d", len(objects))
	}
	if objects[0].Key > objects[1].Key {
		t.Fatalf("expected sorted object keys, got %q then %q", objects[0].Key, objects[1].Key)
	}

	payload, err := repo.ReadLogObject(context.Background(), "/"+objects[0].Key)
	if err != nil {
		t.Fatalf("ReadLogObject returned error: %v", err)
	}
	if strings.TrimSpace(string(payload)) != "{}" {
		t.Fatalf("unexpected payload: %q", string(payload))
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
			repo, err := NewMinIORepo(MinIORepoOptions{Bucket: "logs-bucket", client: store})
			if err != nil {
				t.Fatalf("NewMinIORepo returned error: %v", err)
			}

			err = repo.CheckReady(context.Background())
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

type fakeStoredObject struct {
	data         []byte
	lastModified time.Time
}

type fakeMinIOStore struct {
	mu sync.Mutex

	objects  map[string]fakeStoredObject
	readErr  map[string]error
	writeErr map[string]error
	listErr  error

	bucketExists bool
	bucketErr    error
	writeDelay   time.Duration
	writeCalls   int
}

func newFakeMinIOStore() *fakeMinIOStore {
	return &fakeMinIOStore{
		objects:  make(map[string]fakeStoredObject),
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
	stored, ok := s.objects[key]
	if !ok {
		return nil, errObjectNotFound
	}
	copyData := append([]byte(nil), stored.data...)
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
	s.objects[key] = fakeStoredObject{data: append([]byte(nil), payload...), lastModified: time.Now().UTC()}
	return nil
}

func (s *fakeMinIOStore) ListObjects(ctx context.Context, bucket, prefix string) ([]MinIOObjectInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listErr != nil {
		return nil, s.listErr
	}

	bucketPrefix := bucket + "/"
	fullPrefix := bucket + "/" + prefix
	objects := make([]MinIOObjectInfo, 0)
	for fullKey, stored := range s.objects {
		if !strings.HasPrefix(fullKey, fullPrefix) {
			continue
		}
		objects = append(objects, MinIOObjectInfo{
			Key:          strings.TrimPrefix(fullKey, bucketPrefix),
			LastModified: stored.lastModified,
		})
	}
	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})
	return objects, nil
}

func (s *fakeMinIOStore) putRaw(bucket, object string, payload []byte, modified time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := fmt.Sprintf("%s/%s", bucket, object)
	s.objects[key] = fakeStoredObject{data: append([]byte(nil), payload...), lastModified: modified.UTC()}
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
