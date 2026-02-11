package util

import "time"

const (
	ProfileDir               = "PROFILE_DIR"
	ProfileName              = "PROFILE_NAME"
	ProfileAddr              = "PROFILE_ADDR"
	ProfileEnable            = "PROFILE_ENABLED"
	ProfileCapture           = "PROFILE_CAPTURE"
	KafkaStartupCheckTimeout = 3 * time.Second
)

const (
	DefaultProfileDir  = "profiles"
	DefaultProfileAddr = ":6060"
)

const (
	DateLayout = "2006-01-02"
	HourLayout = "15"
)
