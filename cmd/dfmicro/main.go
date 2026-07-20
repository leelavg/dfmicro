package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"dfmicro/internal/app"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

func main() {
	logger := support.NewLogger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := app.Command(logger, execx.OSRunner{})
	if err := cmd.Run(ctx, os.Args); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}
