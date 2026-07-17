package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"dfmicro/internal/lore"
	"dfmicro/internal/support"
)

func main() {
	logger := support.NewLogger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := lore.Command(logger).Run(ctx, os.Args); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}
