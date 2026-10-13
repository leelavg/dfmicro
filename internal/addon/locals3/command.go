package locals3

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

const (
	containerName = "dfmicro-local-s3"
	volumeName    = "dfmicro-local-s3-data"
)

//go:embed defaults.json
var embeddedDefaults []byte

type manager struct {
	logger *slog.Logger
	runner execx.Runner
}

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	m := &manager{logger: logger, runner: runner}
	var cfg struct {
		Image string `json:"image"`
	}
	if err := json.Unmarshal(embeddedDefaults, &cfg); err != nil {
		panic(err)
	}
	return &cli.Command{
		Name:   "local-s3",
		Usage:  "Manage the standalone local S3 service",
		Action: support.UnknownSubcommand,
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Start the standalone local-s3 service",
				UsageText: `Starts a standalone container on dfmicro's default bridge network without publishing a host port. Data is kept in a named volume.
Requires a cluster-created default bridge network.

Use configure to publish a Service in a cluster.
The service allows anonymous access until accounts are configured.`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "image",
						Usage: "Container image for local-s3",
						Value: cfg.Image,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return m.install(ctx, cmd.String("image"))
				},
			},
			{
				Name:  "configure",
				Usage: "Publish local-s3 in a cluster",
				UsageText: `--attach-to creates s3.default.svc pointing to local-s3 on the cluster's Podman network.
--detach-from removes that Service and EndpointSlice.`,
				MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
					{
						Flags: [][]cli.Flag{
							{&cli.StringFlag{Name: "attach-to", Usage: "Attach S3 Services to this dfmicro cluster"}},
							{&cli.StringFlag{Name: "detach-from", Usage: "Remove local-s3 Services from this dfmicro cluster"}},
						},
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cluster := cmd.String("attach-to"); cluster != "" {
						return m.attach(ctx, cluster)
					}
					return m.detach(ctx, cmd.String("detach-from"))
				},
			},
			{
				Name:  "uninstall",
				Usage: "Remove local-s3 and its cluster Services",
				UsageText: `Configured clusters must be reachable so their Services can be removed.
The data volume is preserved unless --cleanup is passed.`,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "cleanup",
						Usage: "Also delete the local-s3 data volume",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return m.uninstall(ctx, cmd.Bool("cleanup"))
				},
			},
		},
	}
}

func (m *manager) install(ctx context.Context, image string) error {
	exists, err := support.ContainerExists(ctx, m.runner, containerName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("container %q already exists", containerName)
	}

	if err := m.ensureVolume(ctx); err != nil {
		return err
	}
	network := rootconfig.Load().BridgeName
	args := []string{
		"run", "--detach", "--name", containerName,
		"--network", network,
		"--label", "created-by=dfmicro",
		"--volume", volumeName + ":/data",
		"--env", "S3_DATA_DIR=/data",
		image,
	}
	m.logger.Info("starting local S3 service", "image", image, "volume", volumeName)
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, args...); err != nil {
		return fmt.Errorf("start local-s3 container: %w", err)
	}
	m.logger.Info("waiting for local S3 container", "container", containerName)
	var address string
	err = support.Poll(ctx, time.Second, time.Minute, func(ctx context.Context) (bool, error) {
		format := fmt.Sprintf(`{{.State.Status}} {{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{end}}`, network)
		result, err := support.RunPodmanPrivileged(ctx, m.runner, "inspect", "--format", format, containerName)
		if err != nil {
			return false, err
		}
		state := strings.Fields(result.Stdout)
		if len(state) != 2 || state[0] != "running" || state[1] == "" {
			return false, nil
		}
		address = state[1]
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("wait for local-s3 container: %w", err)
	}
	m.logger.Info("local S3 service started", "endpoint", fmt.Sprintf("http://%s:9000", address), "access", "anonymous")
	return nil
}

func (m *manager) ensureVolume(ctx context.Context) error {
	exists, err := m.volumeExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return m.verifyVolume(ctx)
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner,
		"volume", "create", "--label", "created-by=dfmicro", volumeName); err != nil {
		return fmt.Errorf("create local-s3 data volume: %w", err)
	}
	return nil
}

func (m *manager) attach(ctx context.Context, cluster string) error {
	clusterConfig, err := rootconfig.ReadClusterConfig(cluster)
	if err != nil {
		return fmt.Errorf("read cluster config for %q: %w", cluster, err)
	}
	if clusterConfig.Kubeconfig == "" {
		return fmt.Errorf("cluster %q has no kubeconfig", cluster)
	}
	if clusterConfig.BridgeName == "" {
		return fmt.Errorf("cluster %q has no Podman network configured", cluster)
	}
	if !support.ContainerConnectedToNetwork(ctx, m.runner, clusterConfig.BridgeName, containerName) {
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "network", "connect", clusterConfig.BridgeName, containerName); err != nil {
			return fmt.Errorf("connect local-s3 to cluster %q network %q: %w", cluster, clusterConfig.BridgeName, err)
		}
	}
	address, err := support.GetContainerIP(ctx, m.runner, clusterConfig.BridgeName, containerName)
	if err != nil {
		return err
	}
	clusters, err := loadState()
	if err != nil {
		return err
	}
	clusters = addCluster(clusters, cluster)
	if err := saveState(clusters); err != nil {
		return err
	}
	manifest, err := support.Render(serviceTmpl, map[string]string{"Address": address})
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, m.runner, "kubectl", clusterConfig.Kubeconfig, manifest); err != nil {
		return fmt.Errorf("publish local-s3 in cluster %q: %w", cluster, err)
	}
	m.logger.Info("published local-s3 service", "cluster", cluster, "address", address)
	return nil
}

func (m *manager) detach(ctx context.Context, cluster string) error {
	kubeconfig, err := rootconfig.Kubeconfig(cluster)
	if err != nil {
		return fmt.Errorf("read kubeconfig for cluster %q: %w", cluster, err)
	}
	if _, err := m.runner.Run(ctx, "kubectl", "delete", "services,endpointslices", "s3",
		"-n", "default", "--ignore-not-found", "--kubeconfig", kubeconfig); err != nil {
		return fmt.Errorf("remove local-s3 Service from cluster %q: %w", cluster, err)
	}
	clusters, err := loadState()
	if err != nil {
		return err
	}
	return saveState(removeCluster(clusters, cluster))
}

func (m *manager) uninstall(ctx context.Context, cleanup bool) error {
	clusters, err := loadState()
	if err != nil {
		return err
	}
	for len(clusters) > 0 {
		cluster := clusters[0]
		kubeconfig, err := rootconfig.Kubeconfig(cluster)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				clusters = clusters[1:]
				if err := saveState(clusters); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("read kubeconfig for configured cluster %q: %w", cluster, err)
		}
		m.logger.Info("removing local-s3 Services", "cluster", cluster)
		if _, err := m.runner.Run(ctx, "kubectl", "delete", "services,endpointslices",
			"s3", "-n", "default", "--ignore-not-found", "--kubeconfig", kubeconfig); err != nil {
			return fmt.Errorf("remove local-s3 Services from cluster %q before uninstall: %w", cluster, err)
		}
		clusters = clusters[1:]
		if err := saveState(clusters); err != nil {
			return err
		}
	}

	exists, err := support.ContainerExists(ctx, m.runner, containerName)
	if err != nil {
		return err
	}
	if exists {
		label, err := support.RunPodmanPrivileged(ctx, m.runner, "inspect", "--format", `{{index .Config.Labels "created-by"}}`, containerName)
		if err != nil {
			return err
		}
		if strings.TrimSpace(label.Stdout) != "dfmicro" {
			return fmt.Errorf("container %q is not owned by local-s3", containerName)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "rm", "--force", containerName); err != nil {
			return fmt.Errorf("remove local-s3 container: %w", err)
		}
	}
	if cleanup {
		exists, err := m.volumeExists(ctx)
		if err != nil {
			return err
		}
		if exists {
			if err := m.verifyVolume(ctx); err != nil {
				return err
			}
			if _, err := support.RunPodmanPrivileged(ctx, m.runner, "volume", "rm", volumeName); err != nil {
				return fmt.Errorf("remove local-s3 data volume: %w", err)
			}
		}
	}
	return saveState(clusters)
}

func (m *manager) verifyVolume(ctx context.Context) error {
	result, err := support.RunPodmanPrivileged(ctx, m.runner,
		"volume", "inspect", "--format", `{{index .Labels "created-by"}}`, volumeName)
	if err != nil {
		return fmt.Errorf("inspect local-s3 data volume: %w", err)
	}
	if strings.TrimSpace(result.Stdout) != "dfmicro" {
		return fmt.Errorf("Podman volume %q is not owned by local-s3", volumeName)
	}
	return nil
}

func (m *manager) volumeExists(ctx context.Context) (bool, error) {
	_, err := support.RunPodmanPrivileged(ctx, m.runner, "volume", "exists", volumeName)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check local-s3 data volume: %w", err)
}

func addCluster(clusters []string, cluster string) []string {
	if slices.Contains(clusters, cluster) {
		return clusters
	}
	return append(clusters, cluster)
}

func removeCluster(clusters []string, cluster string) []string {
	for i, existing := range clusters {
		if existing == cluster {
			return append(clusters[:i], clusters[i+1:]...)
		}
	}
	return clusters
}
