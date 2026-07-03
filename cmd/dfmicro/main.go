package main

import (
	"context"
	"log/slog"
	"os"

	"dfmicro/internal/app"
	"dfmicro/internal/execx"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	cmd := app.Command(logger.With("component", "app"), execx.OSRunner{})
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}
