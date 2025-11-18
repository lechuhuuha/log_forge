package bootstrap

import (
	"github.com/lechuhuuha/log_forge/internal/metrics"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
	"github.com/lechuhuuha/log_forge/util"
)

// InitObservability wires metrics and optional pprof server.
func InitObservability(logger loggerpkg.Logger) {
	metrics.Init()
	util.MaybeStartPprof(logger)
}
