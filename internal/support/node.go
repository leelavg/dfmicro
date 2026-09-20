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

	for _, mount := range spec.Mounts {
		args = append(args, "--volume", mount)
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

func (n *NodeMgr) Stop(ctx context.Context, name string) error {
	_, err := RunPodmanPrivileged(ctx, n.runner, "stop", "--ignore", "--time", "3", name)
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
	for range 60 {
		if _, err := RunPodmanPrivileged(ctx, n.runner, "exec", "-i", name, "systemctl", "is-active", "-q", "dbus.service"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("the container did not activate the dbus service within 60 seconds")
}

func (n *NodeMgr) WaitReady(ctx context.Context, name string) error {
	for {
		state := n.systemdSubState(ctx, name, "microshift.service")
		if state == "running" {
			if err := n.checkCNI(ctx, name); err == nil {
				return nil
			}
			n.logger.Info("waiting for CNI/network", "container", name)
		} else {
			n.logger.Info("waiting for node readiness", "container", name, "state", state)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
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
