package network

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:   "network",
		Usage:  "Manage multi-cluster networks",
		Action: support.UnknownSubcommand,
		Commands: []*cli.Command{
			createCommand(logger, runner),
			attachCommand(logger, runner),
			detachCommand(logger, runner),
			deleteCommand(logger, runner),
			peerCommand(logger, runner),
			unpeerCommand(logger, runner),
		},
	}
}

func createCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "create",
		Usage: "Create a bridge network for multi-cluster interconnect",
		UsageText: `Create a bridge network that clusters can attach to.

Example:
  dfmicro network create --name backbone --segment-count 5`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "name",
				Usage:    "Network name",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "subnet",
				Usage: "Network subnet in CIDR notation",
				Value: rootconfig.Load().BridgeSubnet,
				Validator: func(s string) error {
					if _, _, err := net.ParseCIDR(s); err != nil {
						return fmt.Errorf("invalid CIDR %s: %w", s, err)
					}
					return nil
				},
			},
			&cli.IntFlag{
				Name:  "segment-count",
				Usage: "Number of IPAM segments for clusters",
				Value: rootconfig.Load().BridgeSegmentCount,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			mgr := newBridgeManager(logger, runner)
			return mgr.create(ctx, bridgeConfig{
				name:         cmd.String("name"),
				subnet:       cmd.String("subnet"),
				segmentCount: cmd.Int("segment-count"),
				stateDir:     bridgeStateDir(),
			})
		},
	}
}

func attachCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "attach",
		Usage: "Attach clusters to a network",
		UsageText: `Attach one or more clusters to a bridge network.

Example:
  dfmicro network attach --cluster first --cluster second --to backbone`,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "cluster",
				Usage:    "Cluster name (repeatable)",
				Required: true,
				Validator: func(names []string) error {
					seen := make(map[string]bool)
					for _, name := range names {
						if seen[name] {
							return fmt.Errorf("duplicate cluster name: %s", name)
						}
						seen[name] = true
					}
					return nil
				},
			},
			&cli.StringFlag{
				Name:     "to",
				Usage:    "Network name to attach to",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "namespace",
				Usage: "Namespace for NAD creation",
				Value: rootconfig.Load().NADNamespace,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			clusterNames := cmd.StringSlice("cluster")
			networkName := cmd.String("to")
			namespace := cmd.String("namespace")

			bridgeState, err := loadBridgeState(bridgeStateDir(), networkName)
			if err != nil {
				return fmt.Errorf("failed to load bridge state for network %s: %w", networkName, err)
			}
			ipam, err := newIPAMManager(bridgeStateDir(), networkName)
			if err != nil {
				return fmt.Errorf("failed to load IPAM state for network %s: %w", networkName, err)
			}
			ops := &multusOps{
				logger: logger,
				runner: runner,
				nad:    newNADManager(logger, runner, "oc"),
				ipam:   ipam,
			}

			for _, clusterName := range clusterNames {
				if err := ops.attach(ctx, bridgeState, clusterName, networkName, namespace); err != nil {
					return err
				}
				if err := ipam.save(bridgeStateDir()); err != nil {
					return fmt.Errorf("failed to save IPAM state for cluster %s: %w", clusterName, err)
				}
			}
			return nil
		},
	}
}

func detachCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "detach",
		Usage: "Detach clusters from a network",
		UsageText: `Detach one or more clusters from a bridge network.

Example:
  dfmicro network detach --cluster first --cluster second --from backbone`,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "cluster",
				Usage:    "Cluster name (repeatable)",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "from",
				Usage:    "Network name to detach from",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "namespace",
				Usage: "Namespace of the NAD to delete",
				Value: rootconfig.Load().NADNamespace,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			clusterNames := cmd.StringSlice("cluster")
			networkName := cmd.String("from")
			namespace := cmd.String("namespace")

			ipam, err := newIPAMManager(bridgeStateDir(), networkName)
			if err != nil {
				return fmt.Errorf("failed to load IPAM state for network %s: %w", networkName, err)
			}
			ops := &multusOps{
				logger: logger,
				runner: runner,
				nad:    newNADManager(logger, runner, "oc"),
				ipam:   ipam,
			}
			for _, clusterName := range clusterNames {
				if err := ops.detach(ctx, clusterName, networkName, namespace); err != nil {
					return err
				}
			}
			return ipam.save(bridgeStateDir())
		},
	}
}

func deleteCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "delete",
		Usage: "Delete a bridge network",
		UsageText: `Delete a bridge network.

Example:
  dfmicro network delete --name backbone`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "name",
				Usage:    "Network name",
				Required: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			networkName := cmd.String("name")
			mgr := newBridgeManager(logger, runner)
			return mgr.delete(ctx, networkName, bridgeStateDir())
		},
	}
}

func peerCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "peer",
		Usage: "Establish direct peering between clusters",
		UsageText: `Establish direct peering between clusters.

Example:
  dfmicro network peer --cluster first --cluster second`,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "cluster",
				Usage:    "Cluster name (repeatable, at least 2 required)",
				Required: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			clusterNames := cmd.StringSlice("cluster")
			if len(clusterNames) < 2 {
				return fmt.Errorf("at least 2 clusters required")
			}
			seen := make(map[string]bool)
			for _, name := range clusterNames {
				if seen[name] {
					return fmt.Errorf("duplicate cluster name: %s", name)
				}
				seen[name] = true
			}
			ops := &peerOps{
				logger: logger,
				runner: runner,
			}
			return ops.peer(ctx, clusterNames)
		},
	}
}

func unpeerCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "unpeer",
		Usage: "Remove direct peering between clusters",
		UsageText: `Remove direct peering between clusters.

Example:
  dfmicro network unpeer --cluster first --cluster second`,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "cluster",
				Usage:    "Cluster name (repeatable, at least 2 required)",
				Required: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			clusterNames := cmd.StringSlice("cluster")
			if len(clusterNames) < 2 {
				return fmt.Errorf("at least 2 clusters required")
			}
			seen := make(map[string]bool)
			for _, name := range clusterNames {
				if seen[name] {
					return fmt.Errorf("duplicate cluster name: %s", name)
				}
				seen[name] = true
			}
			ops := &peerOps{
				logger: logger,
				runner: runner,
			}
			return ops.unpeer(ctx, clusterNames)
		},
	}
}
