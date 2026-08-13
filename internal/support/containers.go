package support

import (
	"context"
	"strings"

	"dfmicro/internal/execx"
)

func clusterContainers(ctx context.Context, runner execx.Runner, name string, all bool) ([]string, error) {
	args := []string{"ps"}
	if all {
		args = append(args, "-a")
	}
	args = append(args, "--filter", "label=part-of="+name, "--format", "{{.Names}}")
	result, err := execx.RunPodmanCommand(ctx, runner, args...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(result.Stdout), nil
}

func AllClusterContainers(ctx context.Context, runner execx.Runner, name string) ([]string, error) {
	return clusterContainers(ctx, runner, name, true)
}

func RunningClusterContainers(ctx context.Context, runner execx.Runner, name string) ([]string, error) {
	return clusterContainers(ctx, runner, name, false)
}
