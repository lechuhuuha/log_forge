package util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

func TestProfileFlags(t *testing.T) {
	cases := []struct {
		name       string
		enableVal  string
		captureVal string
		wantEnable bool
		wantCap    bool
	}{
		{
			name:       "both enabled",
			enableVal:  "true",
			captureVal: "1",
			wantEnable: true,
			wantCap:    true,
		},
		{
			name:       "invalid enable value",
			enableVal:  "not-bool",
			captureVal: "",
			wantEnable: false,
			wantCap:    false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ProfileEnable, tc.enableVal)
			t.Setenv(ProfileCapture, tc.captureVal)
			if got := ProfileEnabled(); got != tc.wantEnable {
				t.Fatalf("unexpected ProfileEnabled: got=%v want=%v", got, tc.wantEnable)
			}
			if got := CaptureProfiles(); got != tc.wantCap {
				t.Fatalf("unexpected CaptureProfiles: got=%v want=%v", got, tc.wantCap)
			}
		})
	}
}

func TestMaybeStartPprof(t *testing.T) {
	cases := []struct {
		name      string
		enableVal string
		addr      string
	}{
		{name: "disabled", enableVal: ""},
		{name: "invalid", enableVal: "bad-bool"},
		{name: "enabled with invalid addr", enableVal: "true", addr: "127.0.0.1:-1"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ProfileEnable, tc.enableVal)
			if tc.addr != "" {
				t.Setenv(ProfileAddr, tc.addr)
			}
			MaybeStartPprof(nil)
			if tc.enableVal == "true" {
				// allow goroutine startup path to execute
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestWithProfiling(t *testing.T) {
	cases := []struct {
		name       string
		action     func() error
		wantErrMsg string
	}{
		{
			name: "propagates action error and writes profiles",
			action: func() error {
				time.Sleep(20 * time.Millisecond)
				return errors.New("action failed")
			},
			wantErrMsg: "action failed",
		},
		{
			name: "successful action still writes profiles",
			action: func() error {
				time.Sleep(10 * time.Millisecond)
				return nil
			},
			wantErrMsg: "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			err := WithProfiling(dir, "test", nil, tc.action)
			if tc.wantErrMsg != "" {
				if err == nil || err.Error() != tc.wantErrMsg {
					t.Fatalf("expected action error %q, got %v", tc.wantErrMsg, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, file := range []string{
				filepath.Join(dir, "cpu_test.prof"),
				filepath.Join(dir, "heap_test.prof"),
				filepath.Join(dir, "goroutine_test.prof"),
			} {
				if _, statErr := os.Stat(file); statErr != nil {
					t.Fatalf("expected profile file %s to exist: %v", file, statErr)
				}
			}
		})
	}
}

func TestStartCPUProfile(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "invalid path returns error",
			path:    filepath.Join(t.TempDir(), "missing", "cpu.prof"),
			wantErr: true,
		},
		{
			name:    "valid path returns stopper",
			path:    filepath.Join(t.TempDir(), "cpu.prof"),
			wantErr: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			stop, err := startCPUProfile(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			stop()
		})
	}
}

func TestDumpProfiles(t *testing.T) {
	cases := []struct {
		name      string
		run       func(path string)
		path      func(t *testing.T) string
		expectOut bool
	}{
		{
			name: "dumpHeap writes file",
			run: func(path string) {
				dumpHeap(path, loggerpkg.NewNop())
			},
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "heap.prof")
			},
			expectOut: true,
		},
		{
			name: "dumpHeap handles create error",
			run: func(path string) {
				dumpHeap(path, loggerpkg.NewNop())
			},
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing", "heap.prof")
			},
			expectOut: false,
		},
		{
			name: "dumpGoroutine writes file",
			run: func(path string) {
				dumpGoroutine(path, loggerpkg.NewNop())
			},
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "goroutine.prof")
			},
			expectOut: true,
		},
		{
			name: "dumpGoroutine handles create error",
			run: func(path string) {
				dumpGoroutine(path, loggerpkg.NewNop())
			},
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "missing", "goroutine.prof")
			},
			expectOut: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path(t)
			tc.run(path)
			_, err := os.Stat(path)
			if tc.expectOut && err != nil {
				t.Fatalf("expected output file to exist: %v", err)
			}
			if !tc.expectOut && err == nil {
				t.Fatalf("expected output file to be missing, got %s", path)
			}
		})
	}
}
