package support

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"

	"dfmicro/internal/execx"
)

var RunPrivileged func(context.Context, execx.Runner, string, ...string) (execx.Result, error)
var WritePrivileged func(context.Context, execx.Runner, string, string, os.FileMode) error
var RunPodman func(context.Context, execx.Runner, ...string) (execx.Result, error)

func init() {
	if runtime.GOOS == "darwin" {
		RunPrivileged = func(ctx context.Context, runner execx.Runner, name string, args ...string) (execx.Result, error) {
			return runner.Run(ctx, "podman", append([]string{"machine", "ssh", "sudo", name}, args...)...)
		}
		WritePrivileged = func(ctx context.Context, runner execx.Runner, path, content string, mode os.FileMode) error {
			_, err := runner.Run(ctx, "podman", "machine", "ssh",
				fmt.Sprintf("printf '%%s' %s | sudo tee %s > /dev/null && sudo chmod %04o %s",
					ShellQuote(content), path, mode, path))
			return err
		}
		RunPodman = func(ctx context.Context, runner execx.Runner, args ...string) (execx.Result, error) {
			return execx.Run(ctx, runner, "podman", args...)
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
		RunPodman = func(ctx context.Context, runner execx.Runner, args ...string) (execx.Result, error) {
			return execx.RunSudo(ctx, runner, "podman", args...)
		}
	}
}

func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
