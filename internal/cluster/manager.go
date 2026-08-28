package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

func checkRootfulMacOS() error {
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

type podmanContainer struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
}

type manager struct {
	cfg    config
	logger *slog.Logger
	runner execx.Runner
}

func newManager(cfg config, logger *slog.Logger, runner execx.Runner) *manager {
	return &manager{
		cfg:    cfg,
		logger: logger,
		runner: runner,
	}
}

func (m *manager) create(ctx context.Context) error {
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

	if err := m.ensurePodmanNetwork(ctx, m.cfg.BridgeName, m.cfg.BridgeSubnet); err != nil {
		return err
	}

	if err := m.addNode(ctx, containerName, m.cfg.BridgeName); err != nil {
		return fmt.Errorf("create node %q: %w", containerName, err)
	}
	if err := m.waitReady(ctx); err != nil {
		return err
	}
	if err := m.trustClusterCIDRs(ctx, containerName); err != nil {
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

func (m *manager) start(ctx context.Context) error {
	containers, err := support.AllClusterContainers(ctx, m.runner, m.cfg.Name)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return fmt.Errorf("cluster %q is not initialized", m.cfg.Name)
	}

	m.logger.Info("starting cluster", "name", m.cfg.Name, "containers", len(containers))
	for _, container := range containers {
		m.logger.Info("starting container", "name", m.cfg.Name, "container", container)
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "start", container); err != nil {
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

func (m *manager) stop(ctx context.Context) error {
	containers, err := support.RunningClusterContainers(ctx, m.runner, m.cfg.Name)
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
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "stop", "--time", "0", container); err != nil {
			m.logger.Warn("failed to stop container", "name", m.cfg.Name, "container", container, "error", err)
		}
	}
	return nil
}

func (m *manager) delete(ctx context.Context) error {
	containers, err := support.AllClusterContainers(ctx, m.runner, m.cfg.Name)
	if err != nil {
		return err
	}

	for _, container := range containers {
		m.logger.Info("stopping container", "name", m.cfg.Name, "container", container)
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "stop", "--time", "0", container); err != nil {
			m.logger.Warn("failed to stop container during delete", "name", m.cfg.Name, "container", container, "error", err)
		}

		m.logger.Info("removing container", "name", m.cfg.Name, "container", container)
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "rm", "-f", "--volumes", container); err != nil {
			m.logger.Warn("failed to remove container during delete", "name", m.cfg.Name, "container", container, "error", err)
		}
	}
	remaining, err := support.AllNetworkContainers(ctx, m.runner, m.cfg.BridgeName)
	if err != nil {
		m.logger.Warn("failed to list network containers", "network", m.cfg.BridgeName, "error", err)
	} else if len(remaining) == 0 {
		m.logger.Info("no containers left on network, removing", "network", m.cfg.BridgeName)
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "network", "rm", m.cfg.BridgeName); err != nil {
			m.logger.Warn("failed to remove network", "network", m.cfg.BridgeName, "error", err)
		}
	}

	if err := m.deleteTopoLVMBackend(ctx); err != nil {
		return err
	}

	if len(containers) == 0 {
		m.logger.Info("cluster not found", "name", m.cfg.Name)
		return nil
	}

	m.logger.Info("cluster removed", "name", m.cfg.Name)
	return nil
}

func (m *manager) createTopoLVMBackend(ctx context.Context) error {
	imageExists := false
	if _, err := os.Stat(m.cfg.LVMDisk); err == nil {
		imageExists = true
		result, err := support.RunPrivileged(ctx, m.runner, "vgs", "--noheadings", "-o", "vg_name", m.cfg.VGName)
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
		if _, err := support.RunPrivileged(ctx, m.runner, "truncate", "--size="+m.cfg.LVMVolSize, m.cfg.LVMDisk); err != nil {
			return err
		}
	}

	result, err := support.RunPrivileged(ctx, m.runner, "losetup", "--find", "--show", "--nooverlap", m.cfg.LVMDisk)
	if err != nil {
		return err
	}
	deviceName := strings.TrimSpace(result.Stdout)
	if deviceName == "" {
		return errors.New("losetup did not return a device name")
	}

	if _, err := support.RunPrivileged(ctx, m.runner, "vgcreate", "-f", "-y", m.cfg.VGName, deviceName); err != nil {
		return err
	}
	if _, err := support.RunPrivileged(ctx, m.runner, "lvcreate", "-l", "99%FREE", "--thinpool", "thin", m.cfg.VGName); err != nil {
		return err
	}

	return nil
}

func (m *manager) deleteTopoLVMBackend(ctx context.Context) error {
	if _, err := os.Stat(m.cfg.LVMDisk); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	m.logger.Info("deleting topolvm backend", "path", m.cfg.LVMDisk)

	// force-remove dm entries for LVs that belong to our VG and are still held by topolvm PVCs;
	// cross-check against lvs output to avoid accidentally touching unrelated dm devices
	if lvResult, err := support.RunPrivileged(ctx, m.runner, "lvs", "--noheadings", "-o", "lv_name", m.cfg.VGName); err == nil {
		knownLVs := map[string]bool{}
		for lv := range strings.SplitSeq(strings.TrimSpace(lvResult.Stdout), "\n") {
			lv = strings.TrimSpace(lv)
			if lv != "" {
				knownLVs[m.cfg.VGName+"-"+lv] = true
			}
		}
		if dmResult, err := support.RunPrivileged(ctx, m.runner, "dmsetup", "ls", "--noheadings", "-C", "-o", "name"); err == nil {
			for name := range strings.SplitSeq(strings.TrimSpace(dmResult.Stdout), "\n") {
				name = strings.TrimSpace(name)
				if knownLVs[name] {
					if _, err := support.RunPrivileged(ctx, m.runner, "dmsetup", "remove", "--force", name); err != nil {
						m.logger.Warn("failed to remove dm device", "device", name, "error", err)
					}
				}
			}
		}

		if _, err := support.RunPrivileged(ctx, m.runner, "lvremove", "--force", "-y", m.cfg.VGName); err != nil {
			m.logger.Warn("failed to remove logical volume", "vg", m.cfg.VGName, "error", err)
		}
		if _, err := support.RunPrivileged(ctx, m.runner, "vgremove", "--force", "-y", m.cfg.VGName); err != nil {
			m.logger.Warn("failed to remove volume group", "vg", m.cfg.VGName, "error", err)
		}
	}

	result, err := support.RunPrivileged(ctx, m.runner, "losetup", "--associated", m.cfg.LVMDisk, "--output", "NAME", "--noheadings")
	if err == nil {
		deviceName := strings.TrimSpace(result.Stdout)
		if deviceName != "" {
			if _, err := support.RunPrivileged(ctx, m.runner, "losetup", "--detach", deviceName); err != nil {
				m.logger.Warn("failed to detach loop device", "device", deviceName, "error", err)
			}
		}
	}

	return os.RemoveAll(filepath.Dir(m.cfg.LVMDisk))
}

func (m *manager) podmanNetworkExists(ctx context.Context, name string) (bool, error) {
	_, err := support.RunPodmanPrivileged(ctx, m.runner, "network", "exists", name)
	if err == nil {
		return true, nil
	}
	var cmdErr *execx.CommandError
	if errors.As(err, &cmdErr) {
		return false, nil
	}
	return false, err
}

func (m *manager) trustClusterCIDRs(ctx context.Context, containerName string) error {
	for _, cidr := range []string{m.cfg.ClusterCIDR, m.cfg.ServiceCIDR} {
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", containerName, "firewall-cmd", "--zone=trusted", "--add-source="+cidr); err != nil {
			return fmt.Errorf("trust CIDR %s: %w", cidr, err)
		}
	}
	return nil
}

func (m *manager) ensurePodmanNetwork(ctx context.Context, name, subnet string) error {
	exists, err := m.podmanNetworkExists(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		m.logger.Info("podman network already exists", "network", name)
		return nil
	}
	m.logger.Info("creating podman network", "network", name, "subnet", subnet)
	args := []string{"network", "create"}
	if subnet != "" {
		args = append(args, "--subnet", subnet)
	}
	args = append(args, name)
	_, err = support.RunPodmanPrivileged(ctx, m.runner, args...)
	return err
}

func (m *manager) containerExists(ctx context.Context, name string) (bool, error) {
	_, err := support.RunPodmanPrivileged(ctx, m.runner, "container", "exists", name)
	if err == nil {
		return true, nil
	}
	var cmdErr *execx.CommandError
	if errors.As(err, &cmdErr) {
		return false, nil
	}
	return false, err
}

func (m *manager) addNode(ctx context.Context, name, networkName string) error {
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

	args = append(args, "--network", networkName, "--dns-search=.")

	if m.cfg.EnableThinpool {
		lvmdConfigPath := filepath.Join(m.cfg.StateDir, "lvmd.yaml")
		var lvmdBuf bytes.Buffer
		if err := template.Must(template.New("").Parse(lvmdConfigTmpl)).Execute(&lvmdBuf, m.cfg); err != nil {
			return err
		}
		if err := os.WriteFile(lvmdConfigPath, lvmdBuf.Bytes(), 0o644); err != nil {
			return err
		}
		args = append(args,
			"--volume", lvmdConfigPath+":/usr/lib/microshift/manifests.d/001-microshift-topolvm/03-lvmd.yaml:ro",
		)
	}

	var clients []string
	if m.cfg.ExposeKubeAPI {
		var err error
		clients, err = getClients()
		if err != nil {
			return err
		}
		args = append(args, "-p", fmt.Sprintf("%d:6443", m.cfg.APIServerPort))
	}

	networkData := struct {
		Clients     []string
		ClusterCIDR string
		ServiceCIDR string
	}{
		Clients:     clients,
		ClusterCIDR: m.cfg.ClusterCIDR,
		ServiceCIDR: m.cfg.ServiceCIDR,
	}

	var networkBuf bytes.Buffer
	tmpl := template.Must(template.New("").Parse(networkConfigTmpl))
	if err := tmpl.Execute(&networkBuf, networkData); err != nil {
		return err
	}

	networkPath := filepath.Join(m.cfg.StateDir, "15-networking.yaml")
	if err := os.WriteFile(networkPath, networkBuf.Bytes(), 0o644); err != nil {
		return err
	}
	args = append(args, "--volume", networkPath+":/etc/microshift/config.d/15-networking.yaml:ro")

	if m.cfg.PowerTuning {
		powerTuningPath := filepath.Join(m.cfg.StateDir, "power-tuning.yaml")
		if err := os.WriteFile(powerTuningPath, []byte(powerTuningConfig), 0o644); err != nil {
			return err
		}
		args = append(args,
			"--volume", powerTuningPath+":/etc/microshift/config.d/10-power-tuning.yaml:ro",
			"--tmpfs", "/var/lib/etcd:size=1G",
		)
	}

	if m.cfg.PullSecret != "" {
		args = append(args, "--volume", m.cfg.PullSecret+":/etc/crio/openshift-pull-secret:ro")
	}

	crioDropinPath := filepath.Join(m.cfg.StateDir, "20-multus-cni-plugins.conf")
	if err := os.WriteFile(crioDropinPath, []byte(multusDropinConfig), 0o644); err != nil {
		return err
	}
	args = append(args, "--volume", crioDropinPath+":/etc/crio/crio.conf.d/20-multus-cni-plugins.conf:ro")

	if len(m.cfg.IDMSFiles) > 0 {
		result, err := convertIDMSFiles(m.cfg.IDMSFiles)
		if err != nil {
			return err
		}
		mirrorsPath := filepath.Join(m.cfg.StateDir, "99-mirrors.conf")
		if err := os.WriteFile(mirrorsPath, []byte(result.registriesConf), 0o644); err != nil {
			return err
		}
		policyPath := filepath.Join(m.cfg.StateDir, "policy.json")
		if err := os.WriteFile(policyPath, []byte(result.policyJSON), 0o644); err != nil {
			return err
		}
		args = append(args,
			"--volume", mirrorsPath+":/etc/containers/registries.conf.d/99-mirrors.conf:ro",
			"--volume", policyPath+":/etc/containers/policy.json:ro",
		)
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
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, args[1:]...); err != nil {
		return err
	}

	return m.waitForDBus(ctx, name)
}

func getClients() ([]string, error) {
	var clients []string

	hostname, err := os.Hostname()
	if err == nil {
		hostname = strings.TrimSpace(hostname)
		if hostname != "" {
			clients = append(clients, hostname)
		}
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return clients, nil
	}

	seen := make(map[string]struct{})
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		if strings.HasPrefix(iface.Name, "podman") {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && ip.To4() != nil {
				ipStr := ip.String()
				if _, exists := seen[ipStr]; !exists {
					seen[ipStr] = struct{}{}
					clients = append(clients, ipStr)
				}
			}
		}
	}
	return clients, nil
}

func (m *manager) waitForDBus(ctx context.Context, name string) error {
	for range 60 {
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", name, "systemctl", "is-active", "-q", "dbus.service"); err == nil {
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

func (m *manager) waitReady(ctx context.Context) error {
	containers, err := support.RunningClusterContainers(ctx, m.runner, m.cfg.Name)
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
			if err := m.checkNodesReady(ctx, containers[0]); err == nil {
				m.logger.Info("all nodes ready")
				return nil
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}

	return errors.New("cluster did not become ready within 10 minutes")
}

func (m *manager) checkNodesReady(ctx context.Context, containerName string) error {
	result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", containerName, "oc", "get", "nodes", "--no-headers")
	if err != nil {
		return err
	}
	for line := range strings.SplitSeq(result.Stdout, "\n") {
		if strings.TrimSpace(line) != "" && strings.Contains(line, "Ready") {
			return nil
		}
	}
	return errors.New("nodes not ready yet")
}

func (m *manager) PrintKubeconfig(ctx context.Context) error {
	data, err := os.ReadFile(m.cfg.DefaultKubeconfigPath)
	if err == nil {
		_, err = os.Stdout.Write(data)
		return err
	}

	m.logger.Info("kubeconfig not found in StateDir, trying container", "path", m.cfg.DefaultKubeconfigPath)
	containers, err := support.RunningClusterContainers(ctx, m.runner, m.cfg.Name)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return fmt.Errorf("cluster %q has no running containers and no kubeconfig in StateDir", m.cfg.Name)
	}
	return m.copyKubeconfig(ctx, containers[0])
}

func (m *manager) copyKubeconfig(ctx context.Context, containerName string) error {
	m.logger.Info("delaying kubeconfig reads to prevent watchdog starvation on systems with hardware watchdog (see FAQ)")
	time.Sleep(5 * time.Second)
	sourcePath := "/var/lib/microshift/resources/kubeadmin/kubeconfig"
	result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", containerName, "cat", sourcePath)
	if err == nil {
		writeKubeconfig(m.cfg.APIServerPort, result.Stdout, m.cfg.DefaultKubeconfigPath)
	}

	if m.cfg.ExposeKubeAPI {
		clients, err := getClients()
		if err == nil && len(clients) > 0 {
			var kubeconfigs []string
			for _, client := range clients {
				time.Sleep(5 * time.Second)
				sourcePath = fmt.Sprintf("/var/lib/microshift/resources/kubeadmin/%s/kubeconfig", client)
				result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", containerName, "cat", sourcePath)
				if err == nil {
					kubeconfigs = append(kubeconfigs, result.Stdout)
					m.logger.Info("kubeconfig found for client", "client", client)
				}
			}
			if len(kubeconfigs) > 0 {
				support.MergeKubeconfigs(m.cfg.Name, m.cfg.APIServerPort, kubeconfigs, clients, m.cfg.DefaultKubeconfigPath)
			}
		}
	} else {
		m.logger.Warn("kubeconfig copied with internal addresses; kubectl from host will not work")
	}

	return nil
}

func writeKubeconfig(port int, content, path string) error {
	if port != 6443 {
		content = strings.ReplaceAll(content, ":6443", fmt.Sprintf(":%d", port))
	}
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

func (m *manager) systemdSubState(ctx context.Context, containerName, unit string) (string, error) {
	result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", containerName, "systemctl", "show", "--property=SubState", "--value", unit)
	if err != nil {
		return "unknown", nil
	}
	return strings.TrimSpace(result.Stdout), nil
}

func listAll(ctx context.Context, logger *slog.Logger, runner execx.Runner) error {
	result, err := support.RunPodmanPrivileged(ctx, runner, "ps", "-a", "--filter", "label=created-by=dfmicro", "--format=json")
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

func (m *manager) exec(ctx context.Context, containerName string) error {
	result, err := support.RunPodmanPrivileged(ctx, m.runner, "ps", "-a", "--filter", "label=part-of="+m.cfg.Name, "--format=json")
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
	return support.RunPodmanPrivilegedInteractive(ctx, m.runner, args...)
}
