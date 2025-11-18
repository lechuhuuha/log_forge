package bootstrap

import (
	"flag"
	"strings"
	"time"

	"github.com/lechuhuuha/log_forge/util"
)

// CLIConfig captures CLI-provided options for starting the server.
type CLIConfig struct {
	Addr                string
	Version             int
	LogsDir             string
	AnalyticsDir        string
	AggregationInterval time.Duration
	ConfigPath          string
}

// ParseFlags reads CLI parameters and returns a Config with defaults applied.
func ParseFlags() CLIConfig {
	addr := flag.String("addr", util.GetEnv(util.HTTPAddr, util.DefaultHTTPAddr), "HTTP listen address")
	versionFlag := flag.Int("version", util.GetIntEnv(util.PipelineVersion, util.DefaultPipelineVersion), "Pipeline version (1 or 2)")
	logsDir := flag.String("logs-dir", util.GetEnv(util.LogsDir, util.DefaultLogsDir), "Directory for raw logs")
	analyticsDir := flag.String("analytics-dir", util.GetEnv(util.AnalyticsDir, util.DefaultAnalyticsDir), "Directory for analytics output")
	aggInterval := flag.Duration("aggregation-interval", util.GetDurationEnv(util.AggregationInterval, util.DefaultAggregationInterval), "Aggregation interval")
	configPath := flag.String("config", "", "Path to YAML config file (overrides related flags)")

	flag.Parse()

	return CLIConfig{
		Addr:                *addr,
		Version:             *versionFlag,
		LogsDir:             *logsDir,
		AnalyticsDir:        *analyticsDir,
		AggregationInterval: *aggInterval,
		ConfigPath:          strings.TrimSpace(*configPath),
	}
}
