package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"dfmicro/internal/app"
	"dfmicro/internal/support"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := support.NewLogger(os.Stdout)
	runner, cleanup, err := support.NewRunner(logger)
	if err != nil {
		logger.Error("failed to get runner", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	cmd := app.Command(logger, runner)
	if err := cmd.Run(ctx, os.Args); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}
