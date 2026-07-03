package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dfmicro/internal/execx"
)

const lvmdConfigTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: topolvm-lvmd-0
  namespace: topolvm-system
data:
  lvmd.yaml: |
        device-classes:
          - name: ssd
            volume-group: %s
            type: thin
            spare-gb: 0
            thin-pool:
              name: thin
              overprovision-ratio: 10.0
`

type podmanContainer struct {
	Names []string `json:"Names"`
}

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
	containerName := m.cfg.Name + "1"

	exists, err := m.containerExists(ctx, containerName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("container %q already exists", containerName)
	}

	if err := os.MkdirAll(m.cfg.StateDir, 0o755); err != nil {
		return err
	}

	if err := m.createTopoLVMBackend(ctx); err != nil {
		return err
	}
	if err := m.ensurePodmanNetwork(ctx, m.cfg.Network); err != nil {
		return err
	}

	subnet, err := m.getSubnet(ctx, m.cfg.Network)
	if err != nil {
		return err
	}

	ipAddress, err := getIPAddress(subnet, 1)
	if err != nil {
		return err
	}

	if err := m.addNode(ctx, containerName, m.cfg.Network, ipAddress); err != nil {
		return fmt.Errorf("create node %q: %w", containerName, err)
	}
	if err := m.waitReady(ctx); err != nil {
		return err
	}
	if err := m.copyKubeconfig(ctx, containerName); err != nil {
		return err
	}
	if err := WriteClusterConfig(m.cfg); err != nil {
		return err
	}

	m.logger.Info("cluster created", "name", m.cfg.Name, "container", containerName, "kubeconfig", m.cfg.DefaultKubeconfigPath)
	return nil
}

func (m *Manager) Start(ctx context.Context) error {
	containers, err := m.getClusterContainers(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return fmt.Errorf("cluster %q is not initialized", m.cfg.Name)
	}

	m.logger.Info("starting cluster", "name", m.cfg.Name, "containers", len(containers))
	for _, container := range containers {
		m.logger.Info("starting container", "name", m.cfg.Name, "container", container)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "start", container); err != nil {
			m.logger.Warn("failed to start container", "name", m.cfg.Name, "container", container, "error", err)
		}
	}

	if err := m.waitReady(ctx); err != nil {
		return err
	}
	if err := m.copyKubeconfig(ctx, containers[0]); err != nil {
		return err
	}

	m.logger.Info("cluster started", "name", m.cfg.Name, "kubeconfig", m.cfg.DefaultKubeconfigPath)
	return nil
}

func (m *Manager) Stop(ctx context.Context) error {
	containers, err := m.getRunningContainers(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		m.logger.Info("no running cluster containers", "name", m.cfg.Name)
		return nil
	}

	m.logger.Info("stopping cluster", "name", m.cfg.Name, "containers", len(containers))
	for _, container := range containers {
		m.logger.Info("stopping container", "name", m.cfg.Name, "container", container)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "stop", "--time", "0", container); err != nil {
			m.logger.Warn("failed to stop container", "name", m.cfg.Name, "container", container, "error", err)
		}
	}
	return nil
}

func (m *Manager) Delete(ctx context.Context) error {
	containers, err := m.getClusterContainers(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		m.logger.Info("cluster not found", "name", m.cfg.Name)
		return nil
	}

	for _, container := range containers {
		m.logger.Info("stopping container", "name", m.cfg.Name, "container", container)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "stop", "--time", "0", container); err != nil {
			m.logger.Warn("failed to stop container during delete", "name", m.cfg.Name, "container", container, "error", err)
		}

		m.logger.Info("removing container", "name", m.cfg.Name, "container", container)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "rm", "-f", "--volumes", container); err != nil {
			m.logger.Warn("failed to remove container during delete", "name", m.cfg.Name, "container", container, "error", err)
		}
	}

	networkExists, err := m.podmanNetworkExists(ctx, m.cfg.Network)
	if err != nil {
		return err
	}
	if networkExists {
		m.logger.Info("removing podman network", "name", m.cfg.Name, "network", m.cfg.Network)
		if _, err := execx.RunSudo(ctx, m.runner, "podman", "network", "rm", m.cfg.Network); err != nil {
			m.logger.Warn("failed to remove podman network", "name", m.cfg.Name, "network", m.cfg.Network, "error", err)
		}
	}

	if err := m.deleteTopoLVMBackend(ctx); err != nil {
		return err
	}

	m.logger.Info("cluster removed", "name", m.cfg.Name)
	return nil
}

func (m *Manager) Status(ctx context.Context) error {
	createdContainers, err := m.getClusterContainers(ctx)
	if err != nil {
		return err
	}
	if len(createdContainers) == 0 {
		return nil
	}

	running, err := m.getRunningContainers(ctx)
	if err != nil {
		return err
	}
	if len(running) == 0 {
		m.logger.Info("cluster is down", "name", m.cfg.Name)
		return nil
	}

	runningSet := make(map[string]struct{}, len(running))
	for _, container := range running {
		runningSet[container] = struct{}{}
	}

	for _, container := range createdContainers {
		if _, ok := runningSet[container]; !ok {
			m.logger.Info("node is not running", "name", m.cfg.Name, "container", container)
		}
	}

	m.logger.Info("cluster is running", "name", m.cfg.Name, "container", running[0], "kubeconfig", m.cfg.DefaultKubeconfigPath)
	result, err := execx.RunSudo(ctx, m.runner, "podman", "exec", "-i", running[0], "kubectl", "get", "nodes,pods", "-A", "-o", "wide")
	if err != nil {
		m.logger.Warn("unable to retrieve cluster status", "name", m.cfg.Name, "error", err)
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
	if _, err := execx.RunSudo(ctx, m.runner, "lvcreate", "-l", "99%FREE", "--thinpool", "thin", m.cfg.VGName); err != nil {
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
		"--volume", "/var/lib/containers:/var/lib/containers",
	}

	for _, device := range []string{"input", "snd", "dri"} {
		if info, err := os.Stat(filepath.Join("/dev", device)); err == nil && info.IsDir() {
			args = append(args, "--tmpfs", filepath.Join("/dev", device))
		}
	}

	args = append(args, "--network", networkName, "--ip", ipAddress, "--dns-search=.")

	lvmdConfigPath := filepath.Join(m.cfg.StateDir, "lvmd.yaml")
	lvmdConfig := fmt.Sprintf(lvmdConfigTemplate, m.cfg.Name)
	if err := os.WriteFile(lvmdConfigPath, []byte(lvmdConfig), 0o644); err != nil {
		return err
	}
	args = append(args,
		"--volume", lvmdConfigPath+":/usr/lib/microshift/manifests.d/001-microshift-topolvm/03-topolvm.yaml:ro",
	)

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
		"--label", "part-of="+m.cfg.Name,
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

func (m *Manager) waitReady(ctx context.Context) error {
	containers, err := m.getRunningContainers(ctx)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return errors.New("no running nodes found")
	}

	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		ready := true
		for _, container := range containers {
			state, err := m.systemdSubState(ctx, container, "microshift.service")
			if err != nil {
				return err
			}
			if state != "running" {
				ready = false
				m.logger.Info("waiting for cluster readiness", "container", container, "state", state)
				break
			}
		}
		if ready {
			m.logger.Info("all nodes ready")
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	return errors.New("cluster did not become ready within 10 minutes")
}

func (m *Manager) copyKubeconfig(ctx context.Context, containerName string) error {
	sourcePath := "/var/lib/microshift/resources/kubeadmin/kubeconfig"
	if m.cfg.ExposeKubeAPI {
		host, err := getHostname()
		if err != nil {
			return err
		}
		sourcePath = fmt.Sprintf("/var/lib/microshift/resources/kubeadmin/%s/kubeconfig", host)
	}

	result, err := execx.RunSudo(ctx, m.runner, "podman", "exec", "-i", containerName, "cat", sourcePath)
	if err != nil {
		return err
	}

	return writeKubeconfig(m.cfg.DefaultKubeconfigPath, result.Stdout)
}

func writeKubeconfig(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	return chownFromSudo(path)
}

func chownFromSudo(path string) error {
	if os.Geteuid() != 0 {
		return nil
	}

	uidValue := os.Getenv("SUDO_UID")
	gidValue := os.Getenv("SUDO_GID")
	if uidValue == "" || gidValue == "" {
		return nil
	}

	uid, err := strconv.Atoi(uidValue)
	if err != nil {
		return fmt.Errorf("parse SUDO_UID %q: %w", uidValue, err)
	}
	gid, err := strconv.Atoi(gidValue)
	if err != nil {
		return fmt.Errorf("parse SUDO_GID %q: %w", gidValue, err)
	}

	return os.Chown(path, uid, gid)
}

func (m *Manager) getClusterContainers(ctx context.Context) ([]string, error) {
	return m.listContainers(ctx, true)
}

func (m *Manager) getRunningContainers(ctx context.Context) ([]string, error) {
	return m.listContainers(ctx, false)
}

func (m *Manager) listContainers(ctx context.Context, all bool) ([]string, error) {
	args := []string{"podman", "ps", "--filter", "label=part-of=" + m.cfg.Name, "--format=json"}
	if all {
		args = []string{"podman", "ps", "-a", "--filter", "label=part-of=" + m.cfg.Name, "--format=json"}
	}

	result, err := execx.RunSudo(ctx, m.runner, args[0], args[1:]...)
	if err != nil {
		return nil, err
	}

	var containers []podmanContainer
	if err := json.Unmarshal([]byte(result.Stdout), &containers); err != nil {
		return nil, fmt.Errorf("parse podman ps json: %w", err)
	}

	names := make([]string, 0, len(containers))
	for _, container := range containers {
		names = append(names, container.Names...)
	}

	return names, nil
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
