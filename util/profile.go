package util

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	runtimePprof "runtime/pprof"
	"strconv"
	"strings"
	"time"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

func MaybeStartPprof(logger loggerpkg.Logger) {
	if logger == nil {
		logger = loggerpkg.NewNop()
	}
	if !ProfileEnabled() {
		return
	}
	addr := GetEnv(ProfileAddr, DefaultProfileAddr)
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		logger.Info("pprof server listening", loggerpkg.F("addr", addr))
		if err := http.ListenAndServe(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("pprof server error", loggerpkg.F("error", err))
		}
	}()
}

func ProfileEnabled() bool {
	return parseBoolEnv(ProfileEnable)
}

func CaptureProfiles() bool {
	return parseBoolEnv(ProfileCapture)
}

func parseBoolEnv(key string) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return false
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return false
	}
	return b
}

// WithProfiling runs the action while capturing CPU and heap profiles under dir.
func WithProfiling(dir, profileName string, logr loggerpkg.Logger, action func() error) error {
	if logr == nil {
		logr = loggerpkg.NewNop()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create profiling dir: %w", err)
	}

	cpuPath := filepath.Join(dir, fmt.Sprintf("cpu_%s.prof", profileName))
	stopCPU, err := startCPUProfile(cpuPath)
	if err != nil {
		logr.Warn("cpu profiling disabled", loggerpkg.F("error", err))
		stopCPU = func() {}
	}
	defer stopCPU()

	start := time.Now()
	err = action()
	duration := time.Since(start)
	logr.Info("profiling completed", loggerpkg.F("profile", profileName), loggerpkg.F("duration", duration.String()))

	heapPath := filepath.Join(dir, fmt.Sprintf("heap_%s.prof", profileName))
	dumpHeap(heapPath, logr)
	goroutinePath := filepath.Join(dir, fmt.Sprintf("goroutine_%s.prof", profileName))
	dumpGoroutine(goroutinePath, logr)

	return err
}

func startCPUProfile(path string) (func(), error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if err := runtimePprof.StartCPUProfile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		runtimePprof.StopCPUProfile()
		f.Close()
	}, nil
}

func dumpHeap(path string, logger loggerpkg.Logger) {
	f, err := os.Create(path)
	if err != nil {
		logger.Error("cannot create heap profile", loggerpkg.F("path", path), loggerpkg.F("error", err))
		return
	}
	defer f.Close()
	if err := runtimePprof.WriteHeapProfile(f); err != nil {
		logger.Error("failed to write heap profile", loggerpkg.F("path", path), loggerpkg.F("error", err))
	}
}

func dumpGoroutine(path string, logger loggerpkg.Logger) {
	f, err := os.Create(path)
	if err != nil {
		logger.Error("cannot create goroutine profile", loggerpkg.F("path", path), loggerpkg.F("error", err))
		return
	}
	defer f.Close()
	if g := runtimePprof.Lookup("goroutine"); g != nil {
		if err := g.WriteTo(f, 0); err != nil {
			logger.Error("failed to write goroutine profile", loggerpkg.F("path", path), loggerpkg.F("error", err))
		}
	} else {
		logger.Warn("goroutine profile lookup returned nil", loggerpkg.F("path", path))
	}
}
