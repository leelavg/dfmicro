package support

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
}

type Node struct {
	logger *slog.Logger
	runner execx.Runner
}

func NewNode(logger *slog.Logger, runner execx.Runner) *Node {
	return &Node{logger: logger, runner: runner}
}

func (n *Node) Create(ctx context.Context, spec NodeSpec) error {
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
	return n.WaitForDBus(ctx, spec.Name)
}

func (n *Node) Stop(ctx context.Context, name string) error {
	_, err := RunPodmanPrivileged(ctx, n.runner, "stop", "--ignore", "--time", "3", name)
	return err
}

func (n *Node) Start(ctx context.Context, name string) error {
	_, err := RunPodmanPrivileged(ctx, n.runner, "start", name)
	return err
}

func (n *Node) Remove(ctx context.Context, name string) error {
	_, err := RunPodmanPrivileged(ctx, n.runner, "rm", "--ignore", "-f", "--volumes", name)
	return err
}

func (n *Node) RemoveState(stateDir, name string) error {
	return os.RemoveAll(filepath.Join(stateDir, name))
}

func (n *Node) WaitForDBus(ctx context.Context, name string) error {
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
