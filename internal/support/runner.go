package support

import (
	"bufio"
	"log/slog"
	"os"
	"slices"

	"dfmicro/internal/execx"
)

func NewRunner(logger *slog.Logger) (execx.Runner, func(), error) {
	runner := &execx.OSRunner{}
	if logPath := os.Getenv("DFMICRO_CMD_LOG"); logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logger.Error("failed to open command log file", "error", err)
			return nil, nil, err
		}
		bw := bufio.NewWriter(f)
		cmdLogger := NewLogger(bw)

		if !slices.Contains(os.Args, "--generate-shell-completion") {
			cmdLogger.Info("starting new run")
		}

		return execx.NewLoggingRunner(runner, cmdLogger),
			func() {
				bw.Flush()
				f.Close()
			}, nil
	}
	return runner, func() {}, nil
}
