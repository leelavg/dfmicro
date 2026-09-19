package support

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

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

func ContainerExists(ctx context.Context, runner execx.Runner, name string) (bool, error) {
	return podmanObjectExists(ctx, runner, "container", name)
}

func NetworkExists(ctx context.Context, runner execx.Runner, name string) (bool, error) {
	return podmanObjectExists(ctx, runner, "network", name)
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

func TrustContainerNetwork(ctx context.Context, runner execx.Runner, container string, sources, interfaces []string) error {
	if len(sources) > 0 {
		args := []string{"exec", container, "firewall-cmd", "--zone=trusted"}
		for _, source := range sources {
			args = append(args, "--add-source="+source)
		}
		if _, err := RunPodmanPrivileged(ctx, runner, args...); err != nil {
			return fmt.Errorf("trust container sources: %w", err)
		}
	}
	for _, iface := range interfaces {
		args := []string{"exec", container, "firewall-cmd", "--zone=trusted", "--add-interface=" + iface}
		if _, err := RunPodmanPrivileged(ctx, runner, args...); err != nil {
			return fmt.Errorf("trust container interface %s: %w", iface, err)
		}
	}
	return nil
}

func WaitForCNI(ctx context.Context, runner execx.Runner, containerName string) error {
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		if err := CheckCNI(ctx, runner, containerName); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return errors.New("CNI/network did not become ready within 10 minutes")
}

func CheckCNI(ctx context.Context, runner execx.Runner, containerName string) error {
	for _, configPath := range []string{
		"/etc/cni/net.d/10-kindnet.conflist",
		"/etc/cni/net.d/00-multus.conf",
	} {
		if _, err := RunPodmanPrivileged(ctx, runner, "exec", containerName, "test", "-s", configPath); err != nil {
			return err
		}
	}
	return nil
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
