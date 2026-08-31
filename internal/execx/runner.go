package execx

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Result struct {
	Stdout string
	Stderr string
}

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
	RunInteractive(ctx context.Context, name string, args ...string) error
}

type CommandError struct {
	name   string
	args   []string
	stdout string
	stderr string
	err    error
}

func (e *CommandError) Error() string {
	var b strings.Builder
	b.WriteString("command failed: ")
	b.WriteString(e.name)
	if len(e.args) > 0 {
		b.WriteString(" ")
		b.WriteString(strings.Join(e.args, " "))
	}
	if e.err != nil {
		b.WriteString(": ")
		b.WriteString(e.err.Error())
	}
	if e.stderr != "" {
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(e.stderr))
	}
	return b.String()
}

func (e *CommandError) Unwrap() error {
	return e.err
}

type PanicRunner struct{}

func (*PanicRunner) Run(_ context.Context, name string, args ...string) (Result, error) {
	panic("PanicRunner: unexpected call to Run: " + name)
}

func (*PanicRunner) RunInteractive(_ context.Context, name string, args ...string) error {
	panic("PanicRunner: unexpected call to RunInteractive: " + name)
}

type OSRunner struct{}

func (*OSRunner) Run(ctx context.Context, name string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err != nil {
		return result, &CommandError{
			name:   name,
			args:   append([]string(nil), args...),
			stdout: result.Stdout,
			stderr: result.Stderr,
			err:    err,
		}
	}

	return result, nil
}

func (*OSRunner) RunInteractive(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func Run(ctx context.Context, runner Runner, name string, args ...string) (Result, error) {
	return runner.Run(ctx, name, args...)
}

func RunSudo(ctx context.Context, runner Runner, name string, args ...string) (Result, error) {
	sudoArgs := append([]string{name}, args...)
	return runner.Run(ctx, "sudo", sudoArgs...)
}

func RunSudoInteractive(ctx context.Context, runner Runner, name string, args ...string) error {
	sudoArgs := append([]string{name}, args...)
	return runner.RunInteractive(ctx, "sudo", sudoArgs...)
}

type LoggingRunner struct {
	inner  Runner
	logger *slog.Logger
}

func NewLoggingRunner(inner Runner, logger *slog.Logger) *LoggingRunner {
	return &LoggingRunner{inner: inner, logger: logger}
}

// TODO: fragile but does the job during dev/testing or the alternate is to
// have a trace func at every call.
func findCaller() (string, int) {
	for skip := 3; skip <= 5; skip++ {
		_, file, line, ok := runtime.Caller(skip)
		if !ok {
			break
		}
		if !strings.Contains(file, "execx") && !strings.Contains(file, "support") {
			return filepath.Base(file), line
		}
	}
	_, file, line, _ := runtime.Caller(3)
	return filepath.Base(file), line
}

func (l *LoggingRunner) Run(ctx context.Context, name string, args ...string) (Result, error) {
	file, line := findCaller()
	l.logger.Info("command", "caller", fmt.Sprintf("%s:%d", file, line), "name", name, "args", args)
	return l.inner.Run(ctx, name, args...)
}

func (l *LoggingRunner) RunInteractive(ctx context.Context, name string, args ...string) error {
	file, line := findCaller()
	l.logger.Info("command", "caller", fmt.Sprintf("%s:%d", file, line), "name", name, "args", args)
	return l.inner.RunInteractive(ctx, name, args...)
}
