package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

var defaultRootConfig = rootconfig.Load()

func nameFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "name",
		Usage: "Cluster name",
		Value: defaultRootConfig.Name,
		Validator: func(s string) error {
			if len(s) > 0 && s[0] == ',' {
				return fmt.Errorf("cluster name cannot start with ','")
			}
			return nil
		},
	}
}

func createFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:     "name",
			Usage:    "Cluster name, used to identify containers and stored config",
			Value:    defaultRootConfig.Name,
			Category: "Cluster:",
			Validator: func(s string) error {
				if len(s) > 0 && s[0] == ',' {
					return fmt.Errorf("cluster name cannot start with ','")
				}
				return nil
			},
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
		&cli.BoolFlag{
			Name:     "no-topolvm",
			Usage:    "Disable TopoLVM storage provisioner (all other topolvm flags are disregarded)",
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
			Name:      "bridge-subnet",
			Usage:     "Network subnet in CIDR notation",
			Value:     rootconfig.Load().BridgeSubnet,
			Validator: support.ValidateIPv4PrivateCIDR,
			Category:  "Network:",
		},
		&cli.StringFlag{
			Name:      "cluster-cidr",
			Usage:     "Pod CIDR for the cluster",
			Value:     defaultRootConfig.ClusterCIDR,
			Category:  "Network:",
			Validator: support.ValidateIPv4PrivateCIDR,
		},
		&cli.StringFlag{
			Name:      "service-cidr",
			Usage:     "Service CIDR for the cluster",
			Value:     defaultRootConfig.ServiceCIDR,
			Category:  "Network:",
			Validator: support.ValidateIPv4PrivateCIDR,
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
		&cli.BoolFlag{
			Name:     "etcd",
			Usage:    "Use etcd storage backend (default: SQLite)",
			Category: "Storage:",
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
  dfmicro cluster create --name dev
  dfmicro cluster create --name odf --lvm-volsize 50G --pull-secret ~/pull-secret.json
  dfmicro cluster create --idms ~/idms-1.yaml --idms ~/idms-2.yaml`,
				Flags: createFlags(),
				MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
					{
						Category: "Storage:",
						Flags: [][]cli.Flag{
							{
								&cli.BoolFlag{
									Name:  "no-thinpool",
									Usage: "Skip thin pool creation and configuration for TopoLVM storage",
								},
							},
							{
								&cli.Float32Flag{
									Name:  "overprovision-ratio",
									Usage: "TopoLVM thin pool overprovision ratio",
									Value: defaultRootConfig.OverprovisionRatio,
								},
							},
						},
					},
				},
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					if support.IsMacOS {
						return ctx, checkRootfulMacOS()
					}
					return ctx, nil
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := newConfigFromCommand(cmd)
					if err != nil {
						return err
					}
					return newManager(cfg, logger, runner).create(ctx)
				},
			},
			{
				Name:      "delete",
				Aliases:   []string{"rm"},
				Usage:     "Delete cluster containers, network, and storage",
				UsageText: "Stops and removes all cluster containers, networking, and storage stack.",
				Flags:     clusterFlags(),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := readClusterConfig(cmd.String("name"))
					if err != nil && !errors.Is(err, os.ErrNotExist) {
						return err
					}
					// TODO: ugly hack, revisit
					cfg.Name = cmd.String("name")
					return newManager(cfg, logger, runner).delete(ctx, errors.Is(err, os.ErrNotExist))
				},
			},
			{
				Name:      "start",
				Usage:     "Start a stopped cluster",
				UsageText: "Use after 'cluster stop' or after a host reboot.",
				Flags:     clusterFlags(),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := readClusterConfig(cmd.String("name"))
					if err != nil {
						return err
					}
					return newManager(cfg, logger, runner).start(ctx)
				},
			},
			{
				Name:      "stop",
				Usage:     "Stop cluster containers without removing them",
				UsageText: "Preserves all state. Resume with 'cluster start'.",
				Flags:     clusterFlags(),
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := readClusterConfig(cmd.String("name"))
					if err != nil {
						return err
					}
					return newManager(cfg, logger, runner).stop(ctx)
				},
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
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := readClusterConfig(cmd.String("name"))
					if err != nil {
						return err
					}
					return newManager(cfg, logger, runner).PrintKubeconfig(ctx)
				},
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
					manager := newManager(cfg, logger, runner)
					return manager.exec(ctx, cmd.String("container"))
				},
			},
		},
	}
}
