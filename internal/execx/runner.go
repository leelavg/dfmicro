package execx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
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

func (PanicRunner) Run(_ context.Context, name string, args ...string) (Result, error) {
	panic("PanicRunner: unexpected call to Run: " + name)
}

func (PanicRunner) RunInteractive(_ context.Context, name string, args ...string) error {
	panic("PanicRunner: unexpected call to RunInteractive: " + name)
}

type OSRunner struct{}

func (OSRunner) Run(ctx context.Context, name string, args ...string) (Result, error) {
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

func Run(ctx context.Context, runner Runner, name string, args ...string) (Result, error) {
	return runner.Run(ctx, name, args...)
}

func RunSudo(ctx context.Context, runner Runner, name string, args ...string) (Result, error) {
	sudoArgs := append([]string{name}, args...)
	return runner.Run(ctx, "sudo", sudoArgs...)
}

func (OSRunner) RunInteractive(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
