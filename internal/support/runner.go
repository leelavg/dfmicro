package support

import (
	"io"
	"log/slog"
	"os"
	"slices"

	"dfmicro/internal/execx"
)

func NewRunner(logger *slog.Logger) (execx.Runner, io.Closer, error) {
	runner := &execx.OSRunner{}
	if logPath := os.Getenv("DFMICRO_CMD_LOG"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logger.Error("failed to open command log file", "error", err)
			return nil, nil, err
		}
		cmdLogger := NewLogger(f)

		if !slices.Contains(os.Args, "--generate-shell-completion") {
			cmdLogger.Info("starting new run", "name", os.Args[0], "args", os.Args[1:])
		}

		return execx.NewLoggingRunner(runner, cmdLogger), f, nil
	}
	return runner, nil, nil
}
