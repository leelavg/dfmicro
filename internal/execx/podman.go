package execx

import (
	"context"
	"runtime"
)

var RunPodmanCommand func(context.Context, Runner, ...string) (Result, error)

func init() {
	if runtime.GOOS == "darwin" {
		RunPodmanCommand = func(ctx context.Context, runner Runner, args ...string) (Result, error) {
			fullArgs := append([]string{"machine", "ssh", "sudo", "podman"}, args...)
			return Run(ctx, runner, "podman", fullArgs...)
		}
	} else {
		RunPodmanCommand = func(ctx context.Context, runner Runner, args ...string) (Result, error) {
			return RunSudo(ctx, runner, "podman", args...)
		}
	}
}
