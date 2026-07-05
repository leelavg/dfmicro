package execx

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

type Result struct {
	Stdout string
	Stderr string
}

type Runner interface {
	Run(ctx context.Context, name string, args ...string) (Result, error)
}

type CommandError struct {
	Name   string
	Args   []string
	Stdout string
	Stderr string
	Err    error
}

func (e *CommandError) Error() string {
	var b strings.Builder
	b.WriteString("command failed: ")
	b.WriteString(e.Name)
	if len(e.Args) > 0 {
		b.WriteString(" ")
		b.WriteString(strings.Join(e.Args, " "))
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	if e.Stderr != "" {
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(e.Stderr))
	}
	return b.String()
}

func (e *CommandError) Unwrap() error {
	return e.Err
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
			Name:   name,
			Args:   append([]string(nil), args...),
			Stdout: result.Stdout,
			Stderr: result.Stderr,
			Err:    err,
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
