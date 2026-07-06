package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dfmicro/internal/app"
	"dfmicro/internal/execx"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey {
				if t, ok := attr.Value.Any().(time.Time); ok {
					return slog.String(slog.TimeKey, t.UTC().Format("2006-01-02T15:04:05Z"))
				}
			}
			return attr
		},
	}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := app.Command(logger, execx.OSRunner{}, fmt.Sprintf("%s (%s)", version, commit))
	if err := cmd.Run(ctx, os.Args); err != nil {
		logger.Error("command failed", "error", err)
		os.Exit(1)
	}
}
