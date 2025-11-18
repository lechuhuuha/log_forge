package bootstrap

import (
	"fmt"

	"go.uber.org/zap"

	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

// InitLogger builds the structured logger used throughout the application.
func InitLogger() (loggerpkg.Logger, func(), error) {
	baseZap, err := zap.NewProduction()
	if err != nil {
		return nil, nil, fmt.Errorf("init logger: %w", err)
	}
	cleanup := func() { _ = baseZap.Sync() }
	return loggerpkg.NewZapLogger(baseZap), cleanup, nil
}
