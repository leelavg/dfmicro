package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dfmicro/internal/execx"
)

var clusterContainerPattern = regexp.MustCompile(`^[a-zA-Z0-9_.-]+[0-9]+$`)

type Manager struct {
	cfg    Config
	logger *slog.Logger
	runner execx.Runner
}

func NewManager(cfg Config, logger *slog.Logger, runner execx.Runner) *Manager {
	return &Manager{
		cfg:    cfg,
		logger: logger.With("component", "cluster"),
		runner: runner,
	}
}

func (m *Manager) Create(ctx context.Context) error {
	containerName := m.cfg.NodeBaseName + "1"

	exists, err := m.containerExists(ctx, containerName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("container %q already exists", containerName)
	}

	if err := m.createTopoLVMBackend(ctx); err != nil {
		return err
	}
	if err := m.ensurePodmanNetwork(ctx, m.cfg.ClusterName); err != nil {
		return err
	}

	subnet, err := m.getSubnet(ctx, m.cfg.ClusterName)
	if err != nil {
		return err
	}

	ipAddress, err := getIPAddress(subnet, 1)
	if err != nil {
		return err
	}

	if err := m.addNode(ctx, containerName, m.cfg.ClusterName, ipAddress); err != nil {
		return fmt.Errorf("create node %q: %w", containerName, err)
	}

	m.logger.Info("cluster created successfully", "container", containerName)
	m.logger.Info("access the node container with sudo podman exec -it " + containerName + " /bin/bash -l")
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	containers, err := m.getClusterContainers(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return errors.New("no cluster containers found")
	}

	m.logger.Info("starting cluster", "containers", len(containers))
	for _, container := range containers {
		m.logger.Info("starting container", "container", container)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "start", container); err != nil {
			m.logger.Warn("failed to start container", "container", container, "error", err)
		}
	}
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	containers, err := m.getRunningContainers(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		m.logger.Info("no running cluster containers")
		return nil
	}

	m.logger.Info("stopping cluster", "containers", len(containers))
	for _, container := range containers {
		m.logger.Info("stopping container", "container", container)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "stop", "--time", "0", container); err != nil {
			m.logger.Warn("failed to stop container", "container", container, "error", err)
		}
	}
	return nil
}

func (m *Manager) Delete(ctx context.Context) error {
	containers, err := m.getClusterContainers(ctx)
	if err != nil {
		return err
	}

	for _, container := range containers {
		m.logger.Info("stopping container", "container", container)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "stop", "--time", "0", container); err != nil {
			m.logger.Warn("failed to stop container during delete", "container", container, "error", err)
		}

		m.logger.Info("removing container", "container", container)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "rm", "-f", "--volumes", container); err != nil {
			m.logger.Warn("failed to remove container during delete", "container", container, "error", err)
		}
	}

	networkExists, err := m.podmanNetworkExists(ctx, m.cfg.ClusterName)
	if err != nil {
		return err
	}
	if networkExists {
		m.logger.Info("removing podman network", "network", m.cfg.ClusterName)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "network", "rm", m.cfg.ClusterName); err != nil {
			m.logger.Warn("failed to remove podman network", "network", m.cfg.ClusterName, "error", err)
		}
	}

	if err := m.deleteTopoLVMBackend(ctx); err != nil {
		return err
	}

	m.logger.Info("cluster destroyed successfully")
	return nil
}

func (m *Manager) Ready(ctx context.Context) error {
	containers, err := m.getRunningContainers(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return errors.New("no running nodes found")
	}

	for _, container := range containers {
		m.logger.Info("checking readiness", "container", container)
		state, err := m.systemdSubState(ctx, container, "microshift.service")
		if err != nil {
			return err
		}
		if state != "running" {
			return fmt.Errorf("node %s is not ready", container)
		}
	}

	m.logger.Info("all nodes running")
	return nil
}

func (m *Manager) Healthy(ctx context.Context) error {
	created, err := m.containerExists(ctx, m.cfg.NodeBaseName+"1")
	if err != nil {
		return err
	}
	if !created {
		return errors.New("cluster is not initialized")
	}

	containers, err := m.getRunningContainers(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return errors.New("cluster is down. no cluster nodes are running")
	}

	for _, container := range containers {
		m.logger.Info("checking health", "container", container)
		state, err := m.systemdSubState(ctx, container, "greenboot-healthcheck")
		if err != nil {
			return err
		}
		if state != "exited" {
			return fmt.Errorf("node %s is not healthy", container)
		}
	}

	m.logger.Info("all nodes healthy")
	return nil
}

func (m *Manager) Status(ctx context.Context) error {
	created, err := m.containerExists(ctx, m.cfg.NodeBaseName+"1")
	if err != nil {
		return err
	}
	if !created {
		return errors.New("cluster is not initialized")
	}

	running, err := m.getRunningContainers(ctx)
	if err != nil {
		return err
	}
	if len(running) == 0 {
		m.logger.Info("cluster is down. no cluster nodes are running")
		return nil
	}

	createdContainers, err := m.getClusterContainers(ctx)
	if err != nil {
		return err
	}

	runningSet := make(map[string]struct{}, len(running))
	for _, container := range running {
		runningSet[container] = struct{}{}
	}

	for _, container := range createdContainers {
		if _, ok := runningSet[container]; !ok {
			m.logger.Info("node is not running", "container", container)
		}
	}

	m.logger.Info("cluster is running", "container", running[0])
	result, err := execx.RunSudo(ctx, m.runner, "podman", "exec", "-i", running[0], "kubectl", "get", "nodes,pods", "-A", "-o", "wide")
	if err != nil {
		m.logger.Warn("unable to retrieve cluster status", "error", err)
		return nil
	}
	fmt.Print(result.Stdout)
	return nil
}

func (m *Manager) createTopoLVMBackend(ctx context.Context) error {
	if _, err := os.Stat(m.cfg.LVMDisk); err == nil {
		m.logger.Info("reusing existing topolvm backend", "path", m.cfg.LVMDisk)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(m.cfg.LVMDisk), 0o755); err != nil {
		return err
	}

	if _, err := execx.RunSudo(ctx, m.runner, "truncate", "--size="+m.cfg.LVMVolSize, m.cfg.LVMDisk); err != nil {
		return err
	}

	result, err := execx.RunSudo(ctx, m.runner, "losetup", "--find", "--show", "--nooverlap", m.cfg.LVMDisk)
	if err != nil {
		return err
	}
	deviceName := strings.TrimSpace(result.Stdout)
	if deviceName == "" {
		return errors.New("losetup did not return a device name")
	}

	if _, err := execx.RunSudo(ctx, m.runner, "vgcreate", "-f", "-y", m.cfg.VGName, deviceName); err != nil {
		return err
	}

	return nil
}

func (m *Manager) deleteTopoLVMBackend(ctx context.Context) error {
	if _, err := os.Stat(m.cfg.LVMDisk); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	m.logger.Info("deleting topolvm backend", "path", m.cfg.LVMDisk)

	if _, err := execx.RunSudo(ctx, m.runner, "lvremove", "-y", m.cfg.VGName); err != nil {
		m.logger.Warn("failed to remove logical volume", "vg", m.cfg.VGName, "error", err)
	}
	if _, err := execx.RunSudo(ctx, m.runner, "vgremove", "-y", m.cfg.VGName); err != nil {
		m.logger.Warn("failed to remove volume group", "vg", m.cfg.VGName, "error", err)
	}

	result, err := execx.RunSudo(ctx, m.runner, "losetup", "-j", m.cfg.LVMDisk)
	if err == nil {
		deviceName := parseLoopDevice(result.Stdout)
		if deviceName != "" {
			if _, err := execx.RunSudo(ctx, m.runner, "losetup", "-d", deviceName); err != nil {
				m.logger.Warn("failed to detach loop device", "device", deviceName, "error", err)
			}
		}
	}

	return os.RemoveAll(filepath.Dir(m.cfg.LVMDisk))
}

func (m *Manager) ensurePodmanNetwork(ctx context.Context, name string) error {
	exists, err := m.podmanNetworkExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		m.logger.Info("podman network already exists", "network", name)
		return nil
	}

	m.logger.Info("creating podman network", "network", name)
	_, err = execx.RunSudo(ctx, m.runner, "podman", "network", "create", name)
	return err
}

func (m *Manager) podmanNetworkExists(ctx context.Context, name string) (bool, error) {
	_, err := execx.RunSudo(ctx, m.runner, "podman", "network", "exists", name)
	if err == nil {
		return true, nil
	}
	var cmdErr *execx.CommandError
	if errors.As(err, &cmdErr) {
		return false, nil
	}
	return false, err
}

func (m *Manager) containerExists(ctx context.Context, name string) (bool, error) {
	_, err := execx.RunSudo(ctx, m.runner, "podman", "container", "exists", name)
	if err == nil {
		return true, nil
	}
	var cmdErr *execx.CommandError
	if errors.As(err, &cmdErr) {
		return false, nil
	}
	return false, err
}

func (m *Manager) getSubnet(ctx context.Context, networkName string) (string, error) {
	result, err := execx.RunSudo(ctx, m.runner, "podman", "network", "inspect", networkName, "--format", "{{range .}}{{range .Subnets}}{{.Subnet}}{{end}}{{end}}")
	if err != nil {
		return "", err
	}

	subnetWithMask := strings.TrimSpace(result.Stdout)
	if subnetWithMask == "" {
		return "", fmt.Errorf("could not determine subnet for network %q", networkName)
	}

	prefix, _, found := strings.Cut(subnetWithMask, "/")
	if !found || prefix == "" {
		return "", fmt.Errorf("invalid subnet returned for network %q: %q", networkName, subnetWithMask)
	}

	return prefix, nil
}

func getIPAddress(subnet string, nodeID int) (string, error) {
	ip := net.ParseIP(strings.TrimSpace(subnet))
	if ip == nil {
		return "", fmt.Errorf("invalid subnet ip: %q", subnet)
	}

	ip = ip.To4()
	if ip == nil {
		return "", fmt.Errorf("only ipv4 subnets are supported: %q", subnet)
	}

	ip[3] = byte(nodeID + 10)
	return ip.String(), nil
}

func (m *Manager) addNode(ctx context.Context, name, networkName, ipAddress string) error {
	args := []string{
		"podman", "run", "--privileged", "-d",
		"--ulimit", "nofile=524288:524288",
		"--tty",
		"--volume", "/dev:/dev",
	}

	for _, device := range []string{"input", "snd", "dri"} {
		if info, err := os.Stat(filepath.Join("/dev", device)); err == nil && info.IsDir() {
			args = append(args, "--tmpfs", filepath.Join("/dev", device))
		}
	}

	args = append(args, "--network", networkName, "--ip", ipAddress, "--dns-search=.")

	if m.cfg.ExposeKubeAPI {
		hostname, err := getHostname()
		if err != nil {
			return err
		}

		content := fmt.Sprintf("apiServer:\n  subjectAltNames:\n    - %s\n", hostname)
		if err := os.WriteFile(m.cfg.ExtraConfig, []byte(content), 0o644); err != nil {
			return err
		}

		args = append(args,
			"-p", fmt.Sprintf("%d:%d", m.cfg.APIServerPort, m.cfg.APIServerPort),
			"--volume", m.cfg.ExtraConfig+":/etc/microshift/config.d/api_server.yaml:ro",
		)
	}

	args = append(args,
		"--tmpfs", "/var/lib/containers",
		"--name", name,
		"--hostname", name,
		m.cfg.Image,
	)

	if _, err := execx.RunSudo(ctx, m.runner, args[0], args[1:]...); err != nil {
		return err
	}

	return m.waitForDBus(ctx, name)
}

func getHostname() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", err
	}
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return "", errors.New("could not determine local hostname")
	}
	return hostname, nil
}

func (m *Manager) waitForDBus(ctx context.Context, name string) error {
	for range 60 {
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "exec", "-i", name, "systemctl", "is-active", "-q", "dbus.service"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("the container did not activate the dbus service within 60 seconds")
}

func (m *Manager) getClusterContainers(ctx context.Context) ([]string, error) {
	result, err := execx.RunSudo(ctx, m.runner, "podman", "ps", "-a", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	return filterClusterContainers(result.Stdout, m.cfg.NodeBaseName), nil
}

func (m *Manager) getRunningContainers(ctx context.Context) ([]string, error) {
	result, err := execx.RunSudo(ctx, m.runner, "podman", "ps", "--format", "{{.Names}}")
	if err != nil {
		return nil, err
	}
	return filterClusterContainers(result.Stdout, m.cfg.NodeBaseName), nil
}

func filterClusterContainers(output, nodeBaseName string) []string {
	lines := strings.Split(output, "\n")
	containers := make([]string, 0, len(lines))
	for _, line := range lines {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		if strings.HasPrefix(name, nodeBaseName) && clusterContainerPattern.MatchString(name) {
			containers = append(containers, name)
		}
	}
	return containers
}

func parseLoopDevice(output string) string {
	line := strings.TrimSpace(output)
	if line == "" {
		return ""
	}
	device, _, found := strings.Cut(line, ":")
	if !found {
		return ""
	}
	return strings.TrimSpace(device)
}

func (m *Manager) systemdSubState(ctx context.Context, containerName, unit string) (string, error) {
	result, err := execx.RunSudo(ctx, m.runner, "podman", "exec", "-i", containerName, "systemctl", "show", "--property=SubState", "--value", unit)
	if err != nil {
		return "unknown", nil
	}
	return strings.TrimSpace(result.Stdout), nil
}
