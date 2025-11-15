package profileutil

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/pprof"
	"time"
)

// WithProfiling runs the action while capturing CPU and heap profiles under dir.
func WithProfiling(dir, profileName string, logger *log.Logger, action func() error) error {
	if logger == nil {
		logger = log.Default()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create profiling dir: %w", err)
	}

	cpuPath := filepath.Join(dir, fmt.Sprintf("cpu_%s.prof", profileName))
	stopCPU, err := startCPUProfile(cpuPath)
	if err != nil {
		logger.Printf("cpu profiling disabled: %v", err)
		stopCPU = func() {}
	}
	defer stopCPU()

	start := time.Now()
	err = action()
	duration := time.Since(start)
	logger.Printf("completed %s in %s", profileName, duration)

	heapPath := filepath.Join(dir, fmt.Sprintf("heap_%s.prof", profileName))
	dumpHeap(heapPath, logger)
	goroutinePath := filepath.Join(dir, fmt.Sprintf("goroutine_%s.prof", profileName))
	dumpGoroutine(goroutinePath, logger)

	return err
}

func startCPUProfile(path string) (func(), error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		pprof.StopCPUProfile()
		f.Close()
	}, nil
}

func dumpHeap(path string, logger *log.Logger) {
	f, err := os.Create(path)
	if err != nil {
		logger.Printf("cannot create heap profile %s: %v", path, err)
		return
	}
	defer f.Close()
	if err := pprof.WriteHeapProfile(f); err != nil {
		logger.Printf("failed to write heap profile %s: %v", path, err)
	}
}

func dumpGoroutine(path string, logger *log.Logger) {
	f, err := os.Create(path)
	if err != nil {
		logger.Printf("cannot create goroutine profile %s: %v", path, err)
		return
	}
	defer f.Close()
	if g := pprof.Lookup("goroutine"); g != nil {
		if err := g.WriteTo(f, 0); err != nil {
			logger.Printf("failed to write goroutine profile %s: %v", path, err)
		}
	} else {
		logger.Printf("goroutine profile lookup returned nil for %s", path)
	}
}
