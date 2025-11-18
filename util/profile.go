package util

import (
	"errors"
	"log"
	"net/http"
	"net/http/pprof"
	"os"
	"strconv"
	"strings"
)

func MaybeStartPprof(logger *log.Logger) {
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
		logger.Printf("pprof server listening on %s", addr)
		if err := http.ListenAndServe(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("pprof server error: %v", err)
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
