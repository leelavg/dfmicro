package support

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dfmicro/internal/execx"
)

type NodeSpec struct {
	Name                string
	Image               string
	Network             string
	StateDir            string
	ClusterName         string
	ShareHostContainers bool
	PullSecret          string
	IDMSFiles           []string
	Mounts              []string
	ExtraArgs           []string
	Trust               NetworkTrust
}

type NetworkTrust struct {
	Sources    []string
	Interfaces []string
}

type NodeMgr struct {
	logger *slog.Logger
	runner execx.Runner
}

func NewNodeMgr(logger *slog.Logger, runner execx.Runner) *NodeMgr {
	return &NodeMgr{logger: logger, runner: runner}
}

func (n *NodeMgr) Create(ctx context.Context, spec NodeSpec) error {
	args := []string{
		"run", "--privileged", "-d",
		"--ulimit", "nofile=524288:524288",
		"--tty",
		"--volume", "/dev:/dev",
	}

	if spec.ShareHostContainers {
		args = append(args, "--volume", "/var/lib/containers:/var/lib/containers")
	}

	for _, device := range []string{"input", "snd", "dri"} {
		if info, err := os.Stat(filepath.Join("/dev", device)); err == nil && info.IsDir() {
			args = append(args, "--tmpfs", filepath.Join("/dev", device))
		}
	}

	args = append(args, "--network", spec.Network, "--dns-search=.")
	args = append(args, spec.ExtraArgs...)

	if spec.PullSecret != "" {
		args = append(args, "--volume", spec.PullSecret+":/etc/crio/openshift-pull-secret:ro")
	}

	if len(spec.IDMSFiles) > 0 {
		result, err := ConvertIDMSFiles(spec.IDMSFiles)
		if err != nil {
			return err
		}
		mirrorsPath := filepath.Join(spec.StateDir, "99-mirrors.conf")
		if err := os.WriteFile(mirrorsPath, []byte(result.RegistriesConf), 0o644); err != nil {
			return err
		}
		policyPath := filepath.Join(spec.StateDir, "policy.json")
		if err := os.WriteFile(policyPath, []byte(result.PolicyJSON), 0o644); err != nil {
			return err
		}
		args = append(args,
			"--volume", mirrorsPath+":/etc/containers/registries.conf.d/99-mirrors.conf:ro",
			"--volume", policyPath+":/etc/containers/policy.json:ro",
		)
	}

	var err error
	args, err = mergeVolumeArgs(args, spec.Mounts)
	if err != nil {
		return err
	}

	args = append(args,
		"--label", "part-of="+spec.ClusterName,
		"--label", "created-by=dfmicro",
		"--name", spec.Name,
		"--hostname", spec.Name,
		spec.Image,
	)

	n.logger.Info("starting node (downloading base image if not cached, ~2GB, may take time)", "name", spec.Name, "image", spec.Image)
	if _, err := RunPodmanPrivileged(ctx, n.runner, args...); err != nil {
		return err
	}
	return n.waitAndTrust(ctx, spec.Name, spec.Trust)
}

func mergeVolumeArgs(args, overrides []string) ([]string, error) {
	var volumes []string
	var otherArgs []string
	volumeIndexes := make(map[string]int)

	for i := 0; i < len(args); i++ {
		if args[i] != "--volume" {
			otherArgs = append(otherArgs, args[i])
			continue
		}
		if i+1 == len(args) {
			return nil, fmt.Errorf("--volume is missing its value")
		}

		mount := args[i+1]
		destination, err := volumeDestination(mount)
		if err != nil {
			return nil, err
		}
		if _, exists := volumeIndexes[destination]; exists {
			return nil, fmt.Errorf("duplicate built-in mount destination %q", destination)
		}
		volumeIndexes[destination] = len(volumes)
		volumes = append(volumes, mount)
		i++
	}

	overridden := make(map[string]struct{}, len(overrides))
	for _, mount := range overrides {
		destination, err := volumeDestination(mount)
		if err != nil {
			return nil, err
		}
		if _, exists := overridden[destination]; exists {
			return nil, fmt.Errorf("multiple custom mounts target %q", destination)
		}
		overridden[destination] = struct{}{}

		if index, exists := volumeIndexes[destination]; exists {
			volumes[index] = mount
			continue
		}
		volumeIndexes[destination] = len(volumes)
		volumes = append(volumes, mount)
	}

	for _, mount := range volumes {
		otherArgs = append(otherArgs, "--volume", mount)
	}
	return otherArgs, nil
}

func volumeDestination(mount string) (string, error) {
	parts := strings.SplitN(mount, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid bind mount %q, expected host-path:container-path[:options]", mount)
	}
	return parts[1], nil
}

func (n *NodeMgr) Stop(ctx context.Context, name string) error {
	_, err := RunPodmanPrivileged(ctx, n.runner, "stop", "--ignore", "--time", "3", name)
	return err
}

func (n *NodeMgr) Pause(ctx context.Context, name string) error {
	_, err := RunPodmanPrivileged(ctx, n.runner, "pause", name)
	return err
}

func (n *NodeMgr) Unpause(ctx context.Context, name string) error {
	_, err := RunPodmanPrivileged(ctx, n.runner, "unpause", name)
	return err
}

func (n *NodeMgr) Start(ctx context.Context, name string, trust NetworkTrust) error {
	if _, err := RunPodmanPrivileged(ctx, n.runner, "start", name); err != nil {
		return err
	}
	return n.waitAndTrust(ctx, name, trust)
}

func (n *NodeMgr) ConfigureServiceRoute(ctx context.Context, name, serviceCIDR, controlIP string) error {
	serviceIP, err := serviceIP(serviceCIDR)
	if err != nil {
		return err
	}
	if _, err := RunPodmanPrivileged(ctx, n.runner, "exec", name, "ip", "route", "replace", serviceIP+"/32", "via", controlIP, "dev", "eth0"); err != nil {
		return fmt.Errorf("route API server IP through control node: %w", err)
	}
	return nil
}

func serviceIP(serviceCIDR string) (string, error) {
	_, ipnet, err := net.ParseCIDR(serviceCIDR)
	if err != nil {
		return "", err
	}
	base := ipnet.IP.To4()
	if base == nil {
		return "", fmt.Errorf("IPv6 service CIDR is not supported")
	}
	ones, _ := ipnet.Mask.Size()
	hostBits := 32 - ones
	if hostBits == 0 {
		return "", fmt.Errorf("service CIDR has no next subnet: %s", serviceCIDR)
	}
	step := uint32(1) << hostBits
	value := binary.BigEndian.Uint32(base)
	if value > ^uint32(0)-step {
		return "", fmt.Errorf("service CIDR has no next subnet: %s", serviceCIDR)
	}
	return addToIP(base, int(step)).String(), nil
}

func addToIP(ip net.IP, offset int) net.IP {
	result := make(net.IP, 4)
	binary.BigEndian.PutUint32(result, binary.BigEndian.Uint32(ip)+uint32(offset))
	return result
}

func (n *NodeMgr) Remove(ctx context.Context, name string) error {
	_, err := RunPodmanPrivileged(ctx, n.runner, "rm", "--ignore", "-f", "--volumes", name)
	return err
}

func (n *NodeMgr) RemoveState(stateDir, name string) error {
	return os.RemoveAll(filepath.Join(stateDir, name))
}

func (n *NodeMgr) waitForDBus(ctx context.Context, name string) error {
	err := Poll(ctx, time.Second, time.Minute, func(ctx context.Context) (bool, error) {
		_, err := RunPodmanPrivileged(ctx, n.runner, "exec", "-i", name, "systemctl", "is-active", "-q", "dbus.service")
		return err == nil, nil
	})
	if err != nil {
		return fmt.Errorf("wait for dbus service: %w", err)
	}
	return nil
}

func (n *NodeMgr) WaitReady(ctx context.Context, name string) error {
	return Poll(ctx, 5*time.Second, 10*time.Minute, func(ctx context.Context) (bool, error) {
		state := n.systemdSubState(ctx, name, "microshift.service")
		if state == "running" {
			if err := n.checkCNI(ctx, name); err == nil {
				return true, nil
			}
			n.logger.Info("waiting for CNI/network", "container", name)
		} else {
			n.logger.Info("waiting for node readiness", "container", name, "state", state)
		}
		return false, nil
	})
}

func (n *NodeMgr) systemdSubState(ctx context.Context, name, unit string) string {
	result, err := RunPodmanPrivileged(ctx, n.runner, "exec", "-i", name, "systemctl", "show", "--property=SubState", "--value", unit)
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(result.Stdout)
}

func (n *NodeMgr) VlanInterfaceExists(ctx context.Context, containerName, devName string) bool {
	result, err := RunPodmanPrivileged(ctx, n.runner, "exec", containerName, "ip", "-br", "link", "show", devName)
	return err == nil && strings.TrimSpace(result.Stdout) != ""
}

func (n *NodeMgr) checkCNI(ctx context.Context, containerName string) error {
	for _, configPath := range []string{
		"/etc/cni/net.d/10-kindnet.conflist",
		"/etc/cni/net.d/00-multus.conf",
	} {
		if _, err := RunPodmanPrivileged(ctx, n.runner, "exec", containerName, "test", "-s", configPath); err != nil {
			return err
		}
	}
	return nil
}

func (n *NodeMgr) waitAndTrust(ctx context.Context, name string, trust NetworkTrust) error {
	if err := n.waitForDBus(ctx, name); err != nil {
		return err
	}
	return n.trustNetwork(ctx, name, trust)
}

func (n *NodeMgr) trustNetwork(ctx context.Context, container string, trust NetworkTrust) error {
	if len(trust.Sources) > 0 {
		args := []string{"exec", container, "firewall-cmd", "--zone=trusted"}
		for _, source := range trust.Sources {
			args = append(args, "--add-source="+source)
		}
		if _, err := RunPodmanPrivileged(ctx, n.runner, args...); err != nil {
			return fmt.Errorf("trust container sources: %w", err)
		}
	}
	for _, iface := range trust.Interfaces {
		args := []string{"exec", container, "firewall-cmd", "--zone=trusted", "--add-interface=" + iface}
		if _, err := RunPodmanPrivileged(ctx, n.runner, args...); err != nil {
			return fmt.Errorf("trust container interface %s: %w", iface, err)
		}
	}
	return nil
}
