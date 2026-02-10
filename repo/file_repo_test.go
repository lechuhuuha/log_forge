package repo

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/model"
)

func TestFileRepoSaveBatch(t *testing.T) {
	cases := []struct {
		name      string
		setup     func(t *testing.T, r *FileRepo) error
		assertion func(t *testing.T, baseDir string, err error)
	}{
		{
			name: "groups by utc date and hour",
			setup: func(_ *testing.T, r *FileRepo) error {
				return r.SaveBatch(context.Background(), []model.LogRecord{
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
			},
			assertion: func(t *testing.T, baseDir string, err error) {
				if err != nil {
					t.Fatalf("SaveBatch returned error: %v", err)
				}

				// First record becomes 03:15 UTC, grouped with the 03:30 record.
				hour03 := filepath.Join(baseDir, "2025-01-02", "03.log.json")
				hour04 := filepath.Join(baseDir, "2025-01-02", "04.log.json")

				lines03 := readLines(t, hour03)
				if len(lines03) != 2 {
					t.Fatalf("expected 2 lines in %s, got %d", hour03, len(lines03))
				}
				lines04 := readLines(t, hour04)
				if len(lines04) != 1 {
					t.Fatalf("expected 1 line in %s, got %d", hour04, len(lines04))
				}

				for _, ln := range append(lines03, lines04...) {
					var rec model.LogRecord
					if err := json.Unmarshal([]byte(ln), &rec); err != nil {
						t.Fatalf("unmarshal line: %v", err)
					}
					if rec.Timestamp.Location() != time.UTC {
						t.Fatalf("expected UTC timestamp, got %v", rec.Timestamp.Location())
					}
				}
			},
		},
		{
			name: "context canceled returns error",
			setup: func(_ *testing.T, r *FileRepo) error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return r.SaveBatch(ctx, []model.LogRecord{{
					Timestamp: time.Now(),
					Path:      "/x",
					UserAgent: "ua",
				}})
			},
			assertion: func(t *testing.T, _ string, err error) {
				if err == nil {
					t.Fatal("expected context cancellation error")
				}
			},
		},
		{
			name: "empty records no-op",
			setup: func(_ *testing.T, r *FileRepo) error {
				return r.SaveBatch(context.Background(), nil)
			},
			assertion: func(t *testing.T, baseDir string, err error) {
				if err != nil {
					t.Fatalf("SaveBatch returned error for empty slice: %v", err)
				}
				entries, readErr := os.ReadDir(baseDir)
				if readErr != nil {
					t.Fatalf("ReadDir returned error: %v", readErr)
				}
				if len(entries) != 0 {
					t.Fatalf("expected no files for empty batch, got %d", len(entries))
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			baseDir := t.TempDir()
			r := NewFileRepo(baseDir, nil)
			err := tc.setup(t, r)
			tc.assertion(t, baseDir, err)
		})
	}
}

func TestLockFor(t *testing.T) {
	cases := []struct {
		name  string
		pathA string
		pathB string
		same  bool
	}{
		{
			name:  "same path returns same mutex",
			pathA: "a/path.log.json",
			pathB: "a/path.log.json",
			same:  true,
		},
		{
			name:  "different path returns different mutex",
			pathA: "a/path.log.json",
			pathB: "another.log.json",
			same:  false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r := NewFileRepo(t.TempDir(), nil)
			a := r.lockFor(tc.pathA)
			b := r.lockFor(tc.pathB)
			if gotSame := a == b; gotSame != tc.same {
				t.Fatalf("unexpected mutex identity: gotSame=%v wantSame=%v", gotSame, tc.same)
			}
		})
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
