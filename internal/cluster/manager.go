package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"dfmicro/internal/execx"
)

var runLVMCommand func(context.Context, execx.Runner, string, ...string) (execx.Result, error)
var runPodmanCommand func(context.Context, execx.Runner, ...string) (execx.Result, error)
var runPodmanInteractive func(context.Context, execx.Runner, ...string) error

func checkMacOSRootful() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "podman", "machine", "inspect", "--format", "{{.Rootful}}")
	result, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to inspect podman machine (is podman machine running?): %w", err)
	}

	if strings.TrimSpace(string(result)) != "true" {
		return fmt.Errorf("podman machine must be running in rootful mode\nPlease recreate with: podman machine init --rootful")
	}
	return nil
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

func init() {
	if runtime.GOOS == "darwin" {
		runLVMCommand = func(ctx context.Context, runner execx.Runner, cmd string, args ...string) (execx.Result, error) {
			return execx.Run(ctx, runner, "podman", "machine", "ssh", "sudo", sshCmd(cmd, args...))
		}
		runPodmanCommand = func(ctx context.Context, runner execx.Runner, args ...string) (execx.Result, error) {
			return execx.Run(ctx, runner, "podman", args...)
		}
		runPodmanInteractive = func(ctx context.Context, runner execx.Runner, args ...string) error {
			return runner.RunInteractive(ctx, "podman", args...)
		}
	} else {
		runLVMCommand = func(ctx context.Context, runner execx.Runner, cmd string, args ...string) (execx.Result, error) {
			return execx.RunSudo(ctx, runner, cmd, args...)
		}
		runPodmanCommand = func(ctx context.Context, runner execx.Runner, args ...string) (execx.Result, error) {
			return execx.RunSudo(ctx, runner, "podman", args...)
		}
		runPodmanInteractive = func(ctx context.Context, runner execx.Runner, args ...string) error {
			sudoArgs := append([]string{"podman"}, args...)
			return runner.RunInteractive(ctx, "sudo", sudoArgs...)
		}
	}
}

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
              overprovision-ratio: %.1f
`

type podmanContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

type Manager struct {
	cfg    Config
	logger *slog.Logger
	runner execx.Runner
}

func NewManager(cfg Config, logger *slog.Logger, runner execx.Runner) *Manager {
	return &Manager{
		cfg:    cfg,
		logger: logger,
		runner: runner,
	}
}

func (m *Manager) Create(ctx context.Context) error {
	containerName := m.cfg.Name + "-1"

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
	if err := writeClusterConfig(m.cfg); err != nil {
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
		if _, err := runPodmanCommand(ctx, m.runner, "start", container); err != nil {
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
		if _, err := runPodmanCommand(ctx, m.runner, "stop", "--time", "0", container); err != nil {
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
		if _, err := runPodmanCommand(ctx, m.runner, "stop", "--time", "0", container); err != nil {
			m.logger.Warn("failed to stop container during delete", "name", m.cfg.Name, "container", container, "error", err)
		}

		m.logger.Info("removing container", "name", m.cfg.Name, "container", container)
		if _, err := runPodmanCommand(ctx, m.runner, "rm", "-f", "--volumes", container); err != nil {
			m.logger.Warn("failed to remove container during delete", "name", m.cfg.Name, "container", container, "error", err)
		}
	}

	networkExists, err := m.podmanNetworkExists(ctx, m.cfg.Network)
	if err != nil {
		return err
	}
	if networkExists {
		m.logger.Info("removing podman network", "name", m.cfg.Name, "network", m.cfg.Network)
		if _, err := runPodmanCommand(ctx, m.runner, "network", "rm", m.cfg.Network); err != nil {
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
	result, err := runPodmanCommand(ctx, m.runner, "exec", "-i", running[0], "kubectl", "get", "nodes,pods", "-A", "-o", "wide")
	if err != nil {
		m.logger.Warn("unable to retrieve cluster status", "name", m.cfg.Name, "error", err)
		return nil
	}
	fmt.Print(result.Stdout)
	return nil
}

func (m *Manager) createTopoLVMBackend(ctx context.Context) error {
	imageExists := false
	if _, err := os.Stat(m.cfg.LVMDisk); err == nil {
		imageExists = true
		result, err := runLVMCommand(ctx, m.runner, "vgs", "--noheadings", "-o", "vg_name", m.cfg.VGName)
		if err == nil && strings.TrimSpace(result.Stdout) == m.cfg.VGName {
			m.logger.Info("reusing existing topolvm backend", "path", m.cfg.LVMDisk, "vg", m.cfg.VGName)
			return nil
		}
		m.logger.Info("image exists but volume group missing, recreating LVM stack", "path", m.cfg.LVMDisk, "vg", m.cfg.VGName)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(m.cfg.LVMDisk), 0o755); err != nil {
		return err
	}

	if !imageExists {
		if _, err := runLVMCommand(ctx, m.runner, "truncate", "--size="+m.cfg.LVMVolSize, m.cfg.LVMDisk); err != nil {
			return err
		}
	}

	result, err := runLVMCommand(ctx, m.runner, "losetup", "--find", "--show", "--nooverlap", m.cfg.LVMDisk)
	if err != nil {
		return err
	}
	deviceName := strings.TrimSpace(result.Stdout)
	if deviceName == "" {
		return errors.New("losetup did not return a device name")
	}

	if _, err := runLVMCommand(ctx, m.runner, "vgcreate", "-f", "-y", m.cfg.VGName, deviceName); err != nil {
		return err
	}
	if _, err := runLVMCommand(ctx, m.runner, "lvcreate", "-l", "99%FREE", "--thinpool", "thin", m.cfg.VGName); err != nil {
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

	if _, err := runLVMCommand(ctx, m.runner, "lvremove", "-y", m.cfg.VGName); err != nil {
		m.logger.Warn("failed to remove logical volume", "vg", m.cfg.VGName, "error", err)
	}
	if _, err := runLVMCommand(ctx, m.runner, "vgremove", "-y", m.cfg.VGName); err != nil {
		m.logger.Warn("failed to remove volume group", "vg", m.cfg.VGName, "error", err)
	}

	result, err := runLVMCommand(ctx, m.runner, "losetup", "--associated", m.cfg.LVMDisk, "--output", "NAME", "--noheadings")
	if err == nil {
		deviceName := strings.TrimSpace(result.Stdout)
		if deviceName != "" {
			if _, err := runLVMCommand(ctx, m.runner, "losetup", "--detach", deviceName); err != nil {
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
	_, err = runPodmanCommand(ctx, m.runner, "network", "create", name)
	return err
}

func (m *Manager) podmanNetworkExists(ctx context.Context, name string) (bool, error) {
	_, err := runPodmanCommand(ctx, m.runner, "network", "exists", name)
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
	_, err := runPodmanCommand(ctx, m.runner, "container", "exists", name)
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
	result, err := runPodmanCommand(ctx, m.runner, "network", "inspect", networkName, "--format", "{{range .}}{{range .Subnets}}{{.Subnet}}{{end}}{{end}}")
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

	if m.cfg.ShareHostContainers {
		args = append(args, "--volume", "/var/lib/containers:/var/lib/containers")
	}

	for _, device := range []string{"input", "snd", "dri"} {
		if info, err := os.Stat(filepath.Join("/dev", device)); err == nil && info.IsDir() {
			args = append(args, "--tmpfs", filepath.Join("/dev", device))
		}
	}

	args = append(args, "--network", networkName, "--ip", ipAddress, "--dns-search=.")

	lvmdConfigPath := filepath.Join(m.cfg.StateDir, "lvmd.yaml")
	lvmdConfig := fmt.Sprintf(lvmdConfigTemplate, m.cfg.Name, m.cfg.OverprovisionRatio)
	if err := os.WriteFile(lvmdConfigPath, []byte(lvmdConfig), 0o644); err != nil {
		return err
	}
	args = append(args,
		"--volume", lvmdConfigPath+":/usr/lib/microshift/manifests.d/001-microshift-topolvm/03-lvmd.yaml:ro",
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

	if m.cfg.PullSecret != "" {
		args = append(args, "--volume", m.cfg.PullSecret+":/etc/crio/openshift-pull-secret:ro")
	}

	if len(m.cfg.IDMSFiles) > 0 {
		registriesConf, err := convertIDMSFiles(m.cfg.IDMSFiles)
		if err != nil {
			return err
		}
		registriesConfPath := filepath.Join(m.cfg.StateDir, "99-mirrors.conf")
		if err := os.WriteFile(registriesConfPath, []byte(registriesConf), 0o644); err != nil {
			return err
		}
		args = append(args, "--volume", registriesConfPath+":/etc/containers/registries.conf.d/99-mirrors.conf:ro")
	}

	for _, mount := range m.cfg.ExtraMounts {
		args = append(args, "--volume", mount)
	}

	args = append(args,
		"--label", "part-of="+m.cfg.Name,
		"--label", "created-by=dfmicro",
		"--name", name,
		"--hostname", name,
		m.cfg.Image,
	)

	m.logger.Info("starting container (downloading base image if not cached, ~2GB, may take time)", "name", name, "image", m.cfg.Image)
	if _, err := runPodmanCommand(ctx, m.runner, args[1:]...); err != nil {
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
		if _, err := runPodmanCommand(ctx, m.runner, "exec", "-i", name, "systemctl", "is-active", "-q", "dbus.service"); err == nil {
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

	result, err := runPodmanCommand(ctx, m.runner, "exec", "-i", containerName, "cat", sourcePath)
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

	result, err := runPodmanCommand(ctx, m.runner, args[1:]...)
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

func (m *Manager) systemdSubState(ctx context.Context, containerName, unit string) (string, error) {
	result, err := runPodmanCommand(ctx, m.runner, "exec", "-i", containerName, "systemctl", "show", "--property=SubState", "--value", unit)
	if err != nil {
		return "unknown", nil
	}
	return strings.TrimSpace(result.Stdout), nil
}

func listAll(ctx context.Context, logger *slog.Logger, runner execx.Runner) error {
	result, err := runPodmanCommand(ctx, runner, "ps", "-a", "--filter", "label=created-by=dfmicro", "--format=json")
	if err != nil {
		return err
	}

	var containers []struct {
		Names  []string          `json:"Names"`
		Labels map[string]string `json:"Labels"`
		State  string            `json:"State"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &containers); err != nil {
		return err
	}

	if len(containers) == 0 {
		return nil
	}

	clusterMap := make(map[string]struct {
		running []string
		stopped []string
	})

	for _, container := range containers {
		clusterName := container.Labels["part-of"]
		info := clusterMap[clusterName]
		for _, name := range container.Names {
			if container.State == "running" {
				info.running = append(info.running, name)
			} else {
				info.stopped = append(info.stopped, name)
			}
		}
		clusterMap[clusterName] = info
	}

	for clusterName, info := range clusterMap {
		logger.Info("found cluster", "name", clusterName, "running", info.running, "stopped", info.stopped)
	}

	return nil
}

func (m *Manager) Exec(ctx context.Context, containerName string) error {
	result, err := runPodmanCommand(ctx, m.runner, "ps", "-a", "--filter", "label=part-of="+m.cfg.Name, "--format=json")
	if err != nil {
		return err
	}

	var containers []podmanContainer
	if err := json.Unmarshal([]byte(result.Stdout), &containers); err != nil {
		return fmt.Errorf("parse podman ps json: %w", err)
	}

	var targetContainer string
	if containerName != "" {
		// Use specified container
		found := false
		for _, c := range containers {
			for _, name := range c.Names {
				if name == containerName {
					if c.State != "running" {
						return fmt.Errorf("container %s is not running (state: %s)", containerName, c.State)
					}
					targetContainer = name
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("container %s not found in cluster %s", containerName, m.cfg.Name)
		}
	} else {
		// Use first running container
		for _, c := range containers {
			if c.State == "running" && len(c.Names) > 0 {
				targetContainer = c.Names[0]
				break
			}
		}
		if targetContainer == "" {
			return fmt.Errorf("no running containers found in cluster %s", m.cfg.Name)
		}
	}

	m.logger.Info("executing shell in container", "container", targetContainer)

	args := []string{"exec", "-it", targetContainer, "sh"}
	return runPodmanInteractive(ctx, m.runner, args...)
}
