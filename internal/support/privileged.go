package support

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dfmicro/internal/execx"
)

var RunPrivileged func(context.Context, execx.Runner, string, ...string) (execx.Result, error)
var WritePrivileged func(context.Context, execx.Runner, string, string, os.FileMode) error
var RunPodmanPrivileged func(context.Context, execx.Runner, ...string) (execx.Result, error)
var RunPodmanPrivilegedInteractive func(context.Context, execx.Runner, ...string) error

func init() {
	if IsMacOS {
		RunPrivileged = func(ctx context.Context, runner execx.Runner, cmd string, args ...string) (execx.Result, error) {
			return runner.Run(ctx, "podman", []string{"machine", "ssh", "sudo", sshCmd(cmd, args...)}...)
		}
		WritePrivileged = func(ctx context.Context, runner execx.Runner, path, content string, mode os.FileMode) error {
			_, err := runner.Run(ctx, "podman", "machine", "ssh",
				fmt.Sprintf("printf '%%s' %s | sudo tee %s > /dev/null && sudo chmod %04o %s",
					shellQuote(content), path, mode, path))
			return err
		}
		RunPodmanPrivileged = func(ctx context.Context, runner execx.Runner, args ...string) (execx.Result, error) {
			return runner.Run(ctx, "podman", args...)
		}
		RunPodmanPrivilegedInteractive = func(ctx context.Context, runner execx.Runner, args ...string) error {
			return runner.RunInteractive(ctx, "podman", args...)
		}
	} else {
		RunPrivileged = execx.RunSudo
		WritePrivileged = func(ctx context.Context, runner execx.Runner, path, content string, mode os.FileMode) error {
			f, err := os.CreateTemp("", "dfmicro-*")
			if err != nil {
				return err
			}
			defer os.Remove(f.Name())
			if _, err := f.WriteString(content); err != nil {
				return err
			}
			f.Close()
			_, err = execx.RunSudo(ctx, runner, "install", fmt.Sprintf("-m%04o", mode), f.Name(), path)
			return err
		}
		RunPodmanPrivileged = func(ctx context.Context, runner execx.Runner, args ...string) (execx.Result, error) {
			return execx.RunSudo(ctx, runner, "podman", args...)
		}
		RunPodmanPrivilegedInteractive = func(ctx context.Context, runner execx.Runner, args ...string) error {
			return execx.RunSudoInteractive(ctx, runner, "podman", args...)
		}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func sshCmd(cmd string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, cmd)
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}
