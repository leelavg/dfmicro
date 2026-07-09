package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"runtime"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"

	"github.com/urfave/cli/v3"
)

var defaultRootConfig = rootconfig.Load()

func nameFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "name",
		Usage: "Cluster name",
		Value: defaultRootConfig.Name,
	}
}

func createFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Usage:    "Cluster name, used to identify containers and stored config",
			Value:    defaultRootConfig.Name,
			Category: "Cluster:",
		},
		&cli.StringFlag{
			Name:     "image",
			Usage:    "MicroShift container image to run (OKD / SCOS build)",
			Value:    defaultRootConfig.Image,
			Category: "Cluster:",
		},
		&cli.StringFlag{
			Name:     "lvm-volsize",
			Usage:    "Size of the sparse loop-device image backing the LVM thin pool for TopoLVM (e.g. 10G, 50G)",
			Value:    defaultRootConfig.LVMVolSize,
			Category: "Storage:",
		},
		&cli.Float32Flag{
			Name:     "overprovision-ratio",
			Usage:    "TopoLVM thin pool overprovision ratio",
			Value:    defaultRootConfig.OverprovisionRatio,
			Category: "Storage:",
		},
		&cli.IntFlag{
			Name:     "api-server-port",
			Usage:    "Host port to expose the Kubernetes API server on (1024-65535)",
			Value:    defaultRootConfig.APIServerPort,
			Category: "Network:",
			Validator: func(v int) error {
				if v < 1024 || v > 65535 {
					return fmt.Errorf("api server port must be between 1024 and 65535: %d", v)
				}
				return nil
			},
		},
		&cli.StringFlag{
			Name:     "network-subnet",
			Usage:    "IPv4 private CIDR for the Podman network (RFC 1918 only)",
			Value:    defaultRootConfig.NetworkSubnet,
			Category: "Network:",
			Validator: func(s string) error {
				ip, _, err := net.ParseCIDR(s)
				if err != nil {
					return fmt.Errorf("invalid CIDR: %w", err)
				}
				if ip.To4() == nil {
					return fmt.Errorf("only IPv4 subnets are supported")
				}
				if !ip.IsPrivate() {
					return fmt.Errorf("subnet must be a private range (RFC 1918)")
				}
				return nil
			},
		},
		&cli.BoolFlag{
			Name:     "no-expose-kubeapi",
			Usage:    "Do not bind the API server port on the host (cluster-internal access only)",
			Category: "Network:",
		},
		&cli.BoolFlag{
			Name:     "no-share-host-containers",
			Usage:    "Do not bind-mount /var/lib/containers from the host (use if the shared containers store gets corrupted)",
			Category: "Mounts (immutable on creation):",
		},
		&cli.BoolFlag{
			Name:     "no-power-tuning",
			Usage:    "Do not apply MicroShift power tuning on create",
			Category: "Mounts (immutable on creation):",
		},
		&cli.StringFlag{
			Name:     "pull-secret",
			Usage:    "Path to a pull secret JSON file for accessing private image registries",
			Category: "Mounts (immutable on creation):",
		},
		&cli.StringSliceFlag{
			Name:     "idms",
			Usage:    "Path to an ImageDigestMirrorSet YAML file for mirror registries (repeatable, merged in order)",
			Category: "Mounts (immutable on creation):",
		},
		&cli.StringSliceFlag{
			Name:     "mount",
			Usage:    "Extra bind mount in Podman format: /host/path:/container/path[:opts] (repeatable)",
			Category: "Mounts (immutable on creation):",
		},
	}
}

func clusterFlags() []cli.Flag {
	return []cli.Flag{nameFlag()}
}

func commandAction(logger *slog.Logger, runner execx.Runner, fn func(context.Context, *Manager) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		cfg, err := newConfigFromCommand(cmd)
		if err != nil {
			return err
		}
		return fn(ctx, NewManager(cfg, logger, runner))
	}
}

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:      "cluster",
		Usage:     "Manage cluster lifecycle",
		UsageText: "Manage MicroShift cluster lifecycle in rootful Podman containers.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
		Commands: []*cli.Command{
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Usage:   "List all clusters",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return listAll(ctx, logger, runner)
				},
			},
			{
				Name:  "create",
				Usage: "Create a cluster, wait until ready, and print connection info",
				UsageText: `Mounts flags are immutable after creation. Delete and recreate to change them.

Examples:
  dfmicro cluster create
  dfmicro cluster create --name dev --network-subnet 10.88.0.0/24
  dfmicro cluster create --name odf --lvm-volsize 50G --pull-secret ~/pull-secret.json
  dfmicro cluster create --idms ~/idms-1.yaml --idms ~/idms-2.yaml`,
				Flags: createFlags(),
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					if runtime.GOOS == "darwin" {
						return ctx, checkMacOSRootful()
					}
					return ctx, nil
				},
				Action: commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Create(ctx) }),
			},
			{
				Name:      "delete",
				Aliases:   []string{"rm"},
				Usage:     "Delete cluster containers, network, and storage",
				UsageText: "Stops and removes all cluster containers, networking, and storage stack.",
				Flags:     clusterFlags(),
				Action:    commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Delete(ctx) }),
			},
			{
				Name:      "start",
				Usage:     "Start a stopped cluster",
				UsageText: "Use after 'cluster stop' or after a host reboot.",
				Flags:     clusterFlags(),
				Action:    commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Start(ctx) }),
			},
			{
				Name:      "stop",
				Usage:     "Stop cluster containers without removing them",
				UsageText: "Preserves all state. Resume with 'cluster start'.",
				Flags:     clusterFlags(),
				Action:    commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Stop(ctx) }),
			},
			{
				Name:      "config",
				Usage:     "Print saved cluster config as JSON",
				UsageText: "Config is recorded at creation time and reflects the flags used.",
				Flags:     []cli.Flag{nameFlag()},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return printClusterConfig(cmd.String("name"))
				},
			},
			{
				Name:  "kubeconfig",
				Usage: "Print kubeconfig for a cluster",
				UsageText: `Pipe to a file or merge into an existing kubeconfig:

  dfmicro cluster kubeconfig > ~/.kube/config
  dfmicro cluster kubeconfig | KUBECONFIG=~/.kube/config:- kubectl config view --merge --flatten > merged.yaml`,
				Flags: []cli.Flag{nameFlag()},
				Action: commandAction(logger, runner, func(ctx context.Context, manager *Manager) error {
					return manager.PrintKubeconfig(ctx)
				}),
			},
			{
				Name:      "exec",
				Usage:     "Open an interactive shell inside the cluster container",
				UsageText: `Useful for running crictl, oc, or kubectl directly against the node.`,
				Flags: []cli.Flag{
					nameFlag(),
					&cli.StringFlag{
						Name:  "container",
						Usage: "Container name (defaults to first running container for the cluster)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := readClusterConfig(cmd.String("name"))
					if err != nil {
						return err
					}
					manager := NewManager(cfg, logger, runner)
					return manager.Exec(ctx, cmd.String("container"))
				},
			},
		},
	}
}
