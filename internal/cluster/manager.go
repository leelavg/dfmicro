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
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

func checkRootfulMacOS(ctx context.Context, runner execx.Runner) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	result, err := runner.Run(timeoutCtx, "podman", "machine", "inspect", "--format", "{{.Rootful}}")
	if err != nil {
		return fmt.Errorf("failed to inspect podman machine (is podman machine running?): %w", err)
	}

	if strings.TrimSpace(result.Stdout) != "true" {
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
	cfg    Config
	logger *slog.Logger
	runner execx.Runner
}

func newManager(cfg Config, logger *slog.Logger, runner execx.Runner) *manager {
	return &manager{
		cfg:    cfg,
		logger: logger,
		runner: runner,
	}
}

func (m *manager) create(ctx context.Context) error {
	containerName := m.cfg.NodeName

	if exists, err := support.ContainerExists(ctx, m.runner, containerName); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("container %q already exists", containerName)
	}

	if err := os.MkdirAll(m.cfg.StateDir, 0o755); err != nil {
		return err
	}

	if m.cfg.EnableTopoLVM {
		topolvm := support.NewTopoLVM(m.runner, m.cfg.StateDir, support.TopoLVMConfig{
			VolumeSize:         m.cfg.LVMVolSize,
			OverprovisionRatio: m.cfg.OverprovisionRatio,
			Thinpool:           m.cfg.EnableThinpool,
		})
		if err := topolvm.CreateBackend(ctx, containerName); err != nil {
			return err
		}
		if err := topolvm.Render(ctx, m.cfg.Name, containerName); err != nil {
			return err
		}
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
	if err := rootconfig.WriteNodesConfig(m.cfg.Name, rootconfig.NodesConfig{Nodes: []rootconfig.NodeRecord{{
		NodeConfig: m.cfg.NodeConfig,
		Kubeconfig: m.cfg.Kubeconfig,
	}}}); err != nil {
		return err
	}

	m.logger.Info("cluster created", "name", m.cfg.Name, "container", containerName, "kubeconfig", m.cfg.Kubeconfig)
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
	node := support.NewNode(m.logger, m.runner)
	for _, container := range containers {
		m.logger.Info("starting container", "name", m.cfg.Name, "container", container)
		if err := node.Start(ctx, container); err != nil {
			m.logger.Warn("failed to start container", "name", m.cfg.Name, "container", container, "error", err)
		}
	}

	if err := m.waitReady(ctx); err != nil {
		return err
	}
	for _, container := range containers {
		if err := m.trustClusterCIDRs(ctx, container); err != nil {
			return err
		}
	}
	if err := m.copyKubeconfig(ctx, containers[0]); err != nil {
		return err
	}

	m.logger.Info("cluster started", "name", m.cfg.Name, "kubeconfig", m.cfg.Kubeconfig)
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
	node := support.NewNode(m.logger, m.runner)
	for _, container := range containers {
		m.logger.Info("stopping container", "name", m.cfg.Name, "container", container)
		if err := node.Stop(ctx, container); err != nil {
			m.logger.Warn("failed to stop container", "name", m.cfg.Name, "container", container, "error", err)
		}
	}
	return nil
}

func (m *manager) delete(ctx context.Context, onlyContainer bool) error {
	containers, err := support.AllClusterContainers(ctx, m.runner, m.cfg.Name)
	if err != nil {
		return err
	}

	node := support.NewNode(m.logger, m.runner)
	for _, container := range containers {
		m.logger.Info("stopping container", "name", m.cfg.Name, "container", container)
		if err := node.Stop(ctx, container); err != nil {
			m.logger.Warn("failed to stop container during delete", "name", m.cfg.Name, "container", container, "error", err)
		}

		m.logger.Info("removing container", "name", m.cfg.Name, "container", container)
		if err := node.Remove(ctx, container); err != nil {
			m.logger.Warn("failed to remove container during delete", "name", m.cfg.Name, "container", container, "error", err)
		}
	}

	if onlyContainer {
		m.logger.Info("deleted container(s) only")
		return nil
	}

	if m.cfg.EnableTopoLVM {
		topolvm := support.NewTopoLVM(m.runner, m.cfg.StateDir, support.TopoLVMConfig{
			VolumeSize:         m.cfg.LVMVolSize,
			OverprovisionRatio: m.cfg.OverprovisionRatio,
			Thinpool:           m.cfg.EnableThinpool,
		})
		if err := topolvm.DeleteBackends(ctx, containers...); err != nil {
			return err
		}
		if err := topolvm.RemoveManifest(); err != nil {
			return err
		}
	}
	for _, container := range containers {
		if err := node.RemoveState(m.cfg.StateDir, container); err != nil {
			return err
		}
	}
	if err := m.removeStateFiles(); err != nil {
		m.logger.Warn("failed to remove cluster state files", "cluster", m.cfg.Name, "error", err)
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

	if len(containers) == 0 {
		m.logger.Info("cluster state removed", "name", m.cfg.Name)
	} else {
		m.logger.Info("cluster removed", "name", m.cfg.Name)
	}
	return nil
}

func (m *manager) removeStateFiles() error {
	entries, err := os.ReadDir(m.cfg.StateDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(m.cfg.StateDir, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", entry.Name(), err)
		}
	}
	return os.Remove(m.cfg.StateDir)
}

func (m *manager) trustClusterCIDRs(ctx context.Context, containerName string) error {
	return support.TrustContainerNetwork(ctx, m.runner, containerName,
		[]string{m.cfg.ClusterCIDR, m.cfg.ServiceCIDR, m.cfg.BridgeSubnet},
		[]string{"eth0"},
	)
}

func (m *manager) ensurePodmanNetwork(ctx context.Context, name, subnet string) error {
	exists, err := support.NetworkExists(ctx, m.runner, name)
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

func (m *manager) addNode(ctx context.Context, name, networkName string) error {
	nodeStateDir := filepath.Join(m.cfg.StateDir, name)
	if err := os.MkdirAll(nodeStateDir, 0o755); err != nil {
		return err
	}

	var extraArgs []string

	kindnetConfigPath := filepath.Join(nodeStateDir, "00-kindnet-config.yaml")
	var kindnetConfigBuf bytes.Buffer
	if err := template.Must(template.New("").Parse(kindnetConfigTmpl)).Execute(&kindnetConfigBuf, m.cfg); err != nil {
		return err
	}
	if err := os.WriteFile(kindnetConfigPath, kindnetConfigBuf.Bytes(), 0o644); err != nil {
		return err
	}
	extraArgs = append(extraArgs,
		"--volume", kindnetConfigPath+":/usr/lib/microshift/manifests.d/000-microshift-kindnet/00-kindnet-config.yaml:ro",
	)

	if m.cfg.EnableTopoLVM {
		topolvm := support.NewTopoLVM(m.runner, m.cfg.StateDir, support.TopoLVMConfig{
			VolumeSize:         m.cfg.LVMVolSize,
			OverprovisionRatio: m.cfg.OverprovisionRatio,
			Thinpool:           m.cfg.EnableThinpool,
		})
		extraArgs = append(extraArgs,
			"--volume", topolvm.ManifestDir()+":/usr/lib/microshift/manifests.d/001-microshift-topolvm:ro",
		)
	}

	var clients []string
	if m.cfg.ExposeKubeAPI {
		var err error
		clients, err = getClients()
		if err != nil {
			return err
		}
		extraArgs = append(extraArgs, "-p", fmt.Sprintf("%d:6443", m.cfg.APIServerPort))
	}

	networkData := struct {
		Clients     []string
		ClusterCIDR string
		ServiceCIDR string
		BaseDomain  string
	}{
		Clients:     clients,
		ClusterCIDR: m.cfg.ClusterCIDR,
		ServiceCIDR: m.cfg.ServiceCIDR,
		BaseDomain:  m.cfg.Name + ".dfmicro.io",
	}

	var networkBuf bytes.Buffer
	tmpl := template.Must(template.New("").Parse(networkConfigTmpl))
	if err := tmpl.Execute(&networkBuf, networkData); err != nil {
		return err
	}

	networkPath := filepath.Join(nodeStateDir, "15-networking.yaml")
	if err := os.WriteFile(networkPath, networkBuf.Bytes(), 0o644); err != nil {
		return err
	}
	extraArgs = append(extraArgs, "--volume", networkPath+":/etc/microshift/config.d/15-networking.yaml:ro")

	if m.cfg.PowerTuning {
		powerTuningPath := filepath.Join(nodeStateDir, "power-tuning.yaml")
		if err := os.WriteFile(powerTuningPath, []byte(powerTuningConfig), 0o644); err != nil {
			return err
		}
		extraArgs = append(extraArgs, "--volume", powerTuningPath+":/etc/microshift/config.d/10-power-tuning.yaml:ro")
	}

	if m.cfg.UseEtcd {
		etcdFlagPath := filepath.Join(nodeStateDir, ".use-etcd")
		if err := os.WriteFile(etcdFlagPath, []byte(""), 0o644); err != nil {
			return err
		}
		extraArgs = append(extraArgs,
			"--volume", etcdFlagPath+":/var/lib/microshift/.use-etcd:ro",
			"--tmpfs", "/var/lib/etcd:size=1G",
		)
	}

	crioDropinPath := filepath.Join(nodeStateDir, "20-multus-cni-plugins.conf")
	if err := os.WriteFile(crioDropinPath, []byte(multusDropinConfig), 0o644); err != nil {
		return err
	}
	extraArgs = append(extraArgs, "--volume", crioDropinPath+":/etc/crio/crio.conf.d/20-multus-cni-plugins.conf:ro")

	return support.NewNode(m.logger, m.runner).Create(ctx, support.NodeSpec{
		Name:                name,
		Image:               m.cfg.Image,
		Network:             networkName,
		StateDir:            nodeStateDir,
		ClusterName:         m.cfg.Name,
		ShareHostContainers: m.cfg.ShareHostContainers,
		PullSecret:          m.cfg.PullSecret,
		IDMSFiles:           m.cfg.IDMSFiles,
		Mounts:              m.cfg.Mounts,
		ExtraArgs:           extraArgs,
	})
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
			if err := support.CheckCNI(ctx, m.runner, containers[0]); err != nil {
				m.logger.Info("waiting for CNI/network", "container", containers[0])
			} else {
				m.logger.Info("CNI/network is ready", "container", containers[0])
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

func (m *manager) PrintKubeconfig(ctx context.Context) error {
	data, err := os.ReadFile(m.cfg.Kubeconfig)
	if err == nil {
		_, err = os.Stdout.Write(data)
		return err
	}

	m.logger.Info("kubeconfig not found in StateDir, trying container", "path", m.cfg.Kubeconfig)
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
	delay := 2 * time.Second
	if m.cfg.UseEtcd {
		delay = 5 * time.Second
	}
	m.logger.Info("delaying kubeconfig reads to prevent watchdog starvation on systems with hardware watchdog (see FAQ)")
	time.Sleep(delay)
	sourcePath := "/var/lib/microshift/resources/kubeadmin/kubeconfig"
	result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", containerName, "cat", sourcePath)
	if err == nil {
		writeKubeconfig(m.cfg.APIServerPort, result.Stdout, m.cfg.Kubeconfig)
	}

	if m.cfg.ExposeKubeAPI {
		clients, err := getClients()
		if err == nil && len(clients) > 0 {
			var kubeconfigs []string
			for _, client := range clients {
				time.Sleep(delay)
				sourcePath = fmt.Sprintf("/var/lib/microshift/resources/kubeadmin/%s/kubeconfig", client)
				result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", containerName, "cat", sourcePath)
				if err == nil {
					kubeconfigs = append(kubeconfigs, result.Stdout)
					m.logger.Info("kubeconfig found for client", "client", client)
				}
			}
			if len(kubeconfigs) > 0 {
				support.MergeKubeconfigs(m.cfg.Name, m.cfg.APIServerPort, kubeconfigs, clients, m.cfg.Kubeconfig)
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
