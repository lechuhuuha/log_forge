package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lechuhuuha/log_forge/model"
)

func TestIngestionServiceAdditionalPaths(t *testing.T) {
	records := []model.LogRecord{{
		Timestamp: time.Now().UTC(),
		Path:      "/x",
		UserAgent: "ua",
	}}

	cases := []struct {
		name    string
		setup   func() *IngestionService
		records []model.LogRecord
		wantErr error
		wantNil bool
		check   func(t *testing.T, svc *IngestionService)
	}{
		{
			name: "mode and close in queue returns stopped",
			setup: func() *IngestionService {
				svc := NewIngestionService(nil, nil, ModeQueue, false, nil)
				return svc
			},
			records: records,
			wantErr: ErrIngestionStopped,
			check: func(t *testing.T, svc *IngestionService) {
				if svc.Mode() != ModeQueue {
					t.Fatalf("expected mode queue, got %v", svc.Mode())
				}
				svc.Close()
			},
		},
		{
			name: "direct mode missing repository",
			setup: func() *IngestionService {
				return NewIngestionService(nil, nil, ModeDirect, false, nil)
			},
			records: records,
			wantErr: errors.New("log store not configured"),
		},
		{
			name: "queue mode missing producer",
			setup: func() *IngestionService {
				return NewIngestionService(nil, nil, ModeQueue, false, nil)
			},
			records: records,
			wantErr: errors.New("producer not configured"),
		},
		{
			name: "empty batch is noop",
			setup: func() *IngestionService {
				return NewIngestionService(nil, nil, ModeDirect, false, nil)
			},
			records: nil,
			wantNil: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			svc := tc.setup()
			if tc.check != nil {
				tc.check(t, svc)
			}
			err := svc.ProcessBatch(context.Background(), tc.records)
			if tc.wantNil {
				if err != nil {
					t.Fatalf("expected nil error, got %v", err)
				}
				return
			}
			if tc.wantErr == ErrIngestionStopped {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected ErrIngestionStopped, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected non-nil error")
			}
		})
	}
}
