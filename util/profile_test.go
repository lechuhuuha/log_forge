package util

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	}{
		{name: "disabled", enableVal: ""},
		{name: "invalid", enableVal: "bad-bool"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ProfileEnable, tc.enableVal)
			MaybeStartPprof(nil)
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
