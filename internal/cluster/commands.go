package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"runtime"

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
			Usage:    "TopoLVM thin pool overprovision ratio; total allocatable storage = volsize * ratio",
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
			Usage:    "IPv4 private CIDR for the dedicated Podman network (RFC 1918 only, e.g. 10.88.0.0/24)",
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
			Usage:    "Do not bind-mount /var/lib/containers from the host (disables image layer reuse, slower pulls)",
			Category: "Mounts (immutable on creation):",
		},
		&cli.StringFlag{
			Name:     "pull-secret",
			Usage:    "Path to a Red Hat pull secret JSON file (required for registries.redhat.io images)",
			Category: "Mounts (immutable on creation):",
		},
		&cli.StringSliceFlag{
			Name:     "idms",
			Usage:    "Path(s) to ImageDigestMirrorSet YAML files for mirror registries; merged in order given",
			Category: "Mounts (immutable on creation):",
		},
		&cli.StringSliceFlag{
			Name:     "mount",
			Usage:    "Extra bind mounts in Podman format: /host/path:/container/path[:opts] (repeatable)",
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
		Name:        "cluster",
		Usage:       "Manage cluster lifecycle",
		Description: "Create, start, stop, and delete MicroShift clusters running in rootful Podman containers.\nEach cluster is identified by name and stores its config under ~/.config/dfmicro/<name>/.",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
		Commands: []*cli.Command{
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Usage:   "List all " + support.BinaryName + " clusters",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return listAll(ctx, logger, runner)
				},
			},
			{
				Name:  "create",
				Usage: "Create a cluster, wait until ready, and print connection info",
				Description: `Pulls the MicroShift container image, creates a dedicated Podman network and LVM thin
pool, starts the node, and waits for the API server and core components to be Ready.

Flags marked "immutable on creation" cannot be changed after the cluster is created.
To change them, delete and recreate the cluster.

Notes:
  - Verified on Linux (Fedora / RHEL). macOS requires a rootful Podman machine.
  - On macOS, run 'podman machine init --rootful && podman machine start' before creating.
  - --pull-secret is required for images from registries.redhat.io (Red Hat content).
  - --idms accepts multiple files; they are merged in the order given.
  - --network-subnet must be an RFC 1918 private range in CIDR notation, IPv4 only.

Examples:
  dfmicro cluster create
  dfmicro cluster create --name dev --network-subnet 10.88.0.0/24
  dfmicro cluster create --name odf --lvm-volsize 50G --pull-secret ~/pull-secret.json
  dfmicro cluster create --idms ~/idms-brew.yaml --idms ~/idms-quay.yaml`,
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
				Name:        "delete",
				Aliases:     []string{"rm"},
				Usage:       "Delete cluster containers, network, and storage",
				Description: "Stops and removes all containers for the cluster, deletes the Podman network, tears down the LVM thin pool, and removes the loop device. The cluster config directory is also removed.",
				Flags:       clusterFlags(),
				Action:      commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Delete(ctx) }),
			},
			{
				Name:        "start",
				Usage:       "Start a stopped cluster",
				Description: "Starts existing cluster containers. Use after 'cluster stop' or after a host reboot. Containers must already exist (created via 'cluster create').",
				Flags:       clusterFlags(),
				Action:      commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Start(ctx) }),
			},
			{
				Name:        "stop",
				Usage:       "Stop cluster containers without removing them",
				Description: "Stops running cluster containers, preserving all state. Resume with 'cluster start'.",
				Flags:       clusterFlags(),
				Action:      commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Stop(ctx) }),
			},
			{
				Name:        "config",
				Usage:       "Print saved cluster config as JSON",
				Description: "Prints the config recorded at creation time (image, network, ports, mounts, etc.).",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Usage:    "Cluster name",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return printClusterConfig(cmd.String("name"))
				},
			},
			{
				Name:  "kubeconfig",
				Usage: "Print kubeconfig for a cluster",
				Description: `Prints the kubeconfig with the API server URL rewritten to match the host port.
Pipe to a file or merge into an existing kubeconfig:

  dfmicro cluster kubeconfig > ~/.kube/config
  dfmicro cluster kubeconfig | KUBECONFIG=~/.kube/config:- kubectl config view --merge --flatten > merged.yaml`,
				Flags: []cli.Flag{nameFlag()},
				Action: commandAction(logger, runner, func(ctx context.Context, manager *Manager) error {
					return manager.PrintKubeconfig(ctx)
				}),
			},
			{
				Name:  "exec",
				Usage: "Open an interactive shell inside the cluster container",
				Description: `Runs an interactive shell inside the running Podman container for the cluster.
Useful for running crictl, oc, or kubectl directly against the node.

Examples:
  dfmicro cluster exec
  dfmicro cluster exec --name dev
  dfmicro cluster exec --container my-container`,
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
