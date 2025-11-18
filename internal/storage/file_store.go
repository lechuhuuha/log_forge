package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/lechuhuuha/log_forge/internal/domain"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/util"
)

// FileLogStore persists logs to disk using an hourly NDJSON layout.
type FileLogStore struct {
	baseDir   string
	logger    loggerpkg.Logger
	mu        sync.Mutex
	fileLocks map[string]*sync.Mutex
}

// NewFileLogStore returns a new file-backed LogStore implementation.
func NewFileLogStore(baseDir string, logr loggerpkg.Logger) *FileLogStore {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	return &FileLogStore{
		baseDir:   baseDir,
		logger:    logr,
		fileLocks: make(map[string]*sync.Mutex),
	}
}

// SaveBatch appends the provided records to the correct hourly files.
func (s *FileLogStore) SaveBatch(ctx context.Context, records []domain.LogRecord) error {
	if len(records) == 0 {
		return nil
	}

	grouped := make(map[string][]domain.LogRecord)
	for _, rec := range records {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t := rec.Timestamp.UTC()
		rec.Timestamp = t
		dateDir := t.Format(util.DateLayout)
		hourFile := fmt.Sprintf("%s.log.json", t.Format(util.HourLayout))
		filePath := filepath.Join(s.baseDir, dateDir, hourFile)
		grouped[filePath] = append(grouped[filePath], rec)
	}

	for path, group := range grouped {
		if err := s.appendRecords(path, group); err != nil {
			return err
		}
	}
	return nil
}

func (s *FileLogStore) appendRecords(path string, records []domain.LogRecord) error {
	lock := s.lockFor(path)
	lock.Lock()
	defer lock.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create log directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer file.Close()

	for _, rec := range records {
		data, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshal log record: %w", err)
		}
		if _, err := file.Write(append(data, '\n')); err != nil {
			return fmt.Errorf("write log record: %w", err)
		}
	}
	return nil
}

func (s *FileLogStore) lockFor(path string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.fileLocks[path]
	if !ok {
		lock = &sync.Mutex{}
		s.fileLocks[path] = lock
	}
	return lock
}
