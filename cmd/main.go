package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lechuhuuha/log_forge/cmd/bootstrap"
	"github.com/lechuhuuha/log_forge/config"
	loggerpkg "github.com/lechuhuuha/log_forge/logger"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)

func main() {
	cfg := config.ParseFlags()

	logg, cleanup, err := bootstrap.InitLogger()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to init logger: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	bootstrap.InitObservability(logg)

	application, err := bootstrap.NewAppWithBuildInfo(cfg, logg, bootstrap.BuildInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build app: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := application.Run(ctx); err != nil {
		logg.Fatal("application exited", loggerpkg.F("error", err))
	}
}
