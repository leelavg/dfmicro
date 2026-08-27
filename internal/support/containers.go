package support

import (
	"context"
	"fmt"
	"strings"

	"dfmicro/internal/execx"
)

const containerNetworkInterfaceTemplate = `
	{{- range $_, $cont := .Containers -}}
		{{- if eq $cont.Name "%s" -}}
			{{- range $ifname, $_ := .Interfaces -}}
				{{- $ifname -}}
			{{- end -}}
		{{- end -}}
	{{- end -}}
`

func clusterContainers(ctx context.Context, runner execx.Runner, name string, all bool) ([]string, error) {
	args := []string{"ps"}
	if all {
		args = append(args, "-a")
	}
	args = append(args, "--filter", "label=part-of="+name, "--format", "{{.Names}}")
	result, err := RunPodmanPrivileged(ctx, runner, args...)
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

func AllNetworkContainers(ctx context.Context, runner execx.Runner, networkName string) ([]string, error) {
	result, err := RunPodmanPrivileged(ctx, runner, "ps", "-a", "--filter", "network="+networkName, "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(result.Stdout), nil
}

func ContainerConnectedToNetwork(ctx context.Context, runner execx.Runner, networkName, containerName string) bool {
	result, err := RunPodmanPrivileged(ctx, runner,
		"network", "inspect", networkName,
		"--format", fmt.Sprintf(containerNetworkInterfaceTemplate, containerName),
	)
	if err != nil {
		return false
	}
	return strings.TrimSpace(result.Stdout) != ""
}

func VlanInterfaceExists(ctx context.Context, runner execx.Runner, containerName, devName string) bool {
	result, err := RunPodmanPrivileged(ctx, runner, "exec", containerName, "ip", "-br", "link", "show", devName)
	return err == nil && strings.TrimSpace(result.Stdout) != ""
}

func GetContainerEth(ctx context.Context, runner execx.Runner, networkName, containerName string) (string, error) {
	result, err := RunPodmanPrivileged(ctx, runner,
		"network", "inspect", networkName,
		"--format", fmt.Sprintf(containerNetworkInterfaceTemplate, containerName),
	)
	if err != nil {
		return "", fmt.Errorf("failed to inspect network %s: %w", networkName, err)
	}

	eth := strings.TrimSpace(result.Stdout)
	if eth == "" {
		return "", fmt.Errorf("container %s not found in network %s", containerName, networkName)
	}
	return eth, nil
}
