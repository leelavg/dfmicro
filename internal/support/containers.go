package support

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
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

func clusterContainers(ctx context.Context, runner execx.Runner, name string, all bool, status string) ([]string, error) {
	args := []string{"ps"}
	if all {
		args = append(args, "-a")
	}
	if status != "" {
		args = append(args, "--filter", "status="+status)
	}
	args = append(args, "--filter", "label=part-of="+name, "--format", "{{.Names}}")
	result, err := RunPodmanPrivileged(ctx, runner, args...)
	if err != nil {
		return nil, err
	}
	return strings.Fields(result.Stdout), nil
}

func AllClusterContainers(ctx context.Context, runner execx.Runner, name string) ([]string, error) {
	return clusterContainers(ctx, runner, name, true, "")
}

func RunningClusterContainers(ctx context.Context, runner execx.Runner, name string) ([]string, error) {
	return clusterContainers(ctx, runner, name, false, "running")
}

func PausedClusterContainers(ctx context.Context, runner execx.Runner, name string) ([]string, error) {
	return clusterContainers(ctx, runner, name, true, "paused")
}

func AllNetworkContainers(ctx context.Context, runner execx.Runner, networkName string) ([]string, error) {
	result, err := RunPodmanPrivileged(ctx, runner, "ps", "-a", "--filter", "network="+networkName, "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	return strings.Fields(result.Stdout), nil
}

func ContainerExists(ctx context.Context, runner execx.Runner, name string) (bool, error) {
	return podmanObjectExists(ctx, runner, "container", name)
}

func NetworkExists(ctx context.Context, runner execx.Runner, name string) (bool, error) {
	return podmanObjectExists(ctx, runner, "network", name)
}

func EnsurePodmanNetwork(ctx context.Context, logger *slog.Logger, runner execx.Runner, name, subnet string) error {
	exists, err := NetworkExists(ctx, runner, name)
	if err != nil {
		return err
	}
	if exists {
		logger.Info("podman network already exists", "network", name)
		return nil
	}
	logger.Info("creating podman network", "network", name, "subnet", subnet)
	args := []string{"network", "create", "--label", "created-by=dfmicro"}
	if subnet != "" {
		args = append(args, "--subnet", subnet)
	}
	args = append(args, name)
	_, err = RunPodmanPrivileged(ctx, runner, args...)
	return err
}

func podmanObjectExists(ctx context.Context, runner execx.Runner, objectType, name string) (bool, error) {
	_, err := RunPodmanPrivileged(ctx, runner, objectType, "exists", name)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
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

func GetContainerIP(ctx context.Context, runner execx.Runner, networkName, containerName string) (string, error) {
	format := fmt.Sprintf("{{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{end}}", networkName)
	result, err := RunPodmanPrivileged(ctx, runner, "inspect", containerName, "--format", format)
	if err != nil {
		return "", fmt.Errorf("failed to inspect container %s: %w", containerName, err)
	}

	address := strings.TrimSpace(result.Stdout)
	if address == "" {
		return "", fmt.Errorf("container %s has no IP on network %s", containerName, networkName)
	}
	return address, nil
}
