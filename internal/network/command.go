package network

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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
			configCommand(logger, runner),
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
  dfmicro network create --name backbone --group-count 5 --subnet 172.30.0.0/16`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "name",
				Usage:    "Network name",
				Required: true,
			},
			&cli.StringFlag{
				Name:      "subnet",
				Usage:     "Network subnet in CIDR notation",
				Required:  true,
				Validator: support.ValidateIPv4PrivateCIDR,
			},
			&cli.IntFlag{
				Name:  "group-count",
				Usage: "Number of IPAM groups for the subnet",
				Value: rootconfig.Load().GroupCount,
				Validator: func(i int) error {
					if i < 1 {
						return fmt.Errorf("invalid count, should be more than 0")
					}
					return nil
				},
			},
			&cli.IntFlag{
				Name:  "cluster-count",
				Usage: "Number of clusters per IPAM group",
				Value: rootconfig.Load().ClustersPerGroup,
				Validator: func(i int) error {
					if i < 1 {
						return fmt.Errorf("invalid count, should be more than 0")
					}
					return nil
				},
			},
			&cli.IntFlag{
				Name:  "reserve-count",
				Usage: "Number of IPs to reserve per group",
				Value: rootconfig.Load().ReservePerGroup,
				Validator: func(i int) error {
					if i < 1 {
						return fmt.Errorf("invalid count, should be more than 0")
					}
					return nil
				},
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			mgr := newBridgeManager(logger, runner)
			return mgr.create(ctx, bridgeConfig{
				name:             cmd.String("name"),
				subnet:           cmd.String("subnet"),
				groupCount:       cmd.Int("group-count"),
				clustersPerGroup: cmd.Int("cluster-count"),
				reservePerGroup:  cmd.Int("reserve-count"),
				stateDir:         networkStateDir(rootconfig.ConfigDir()),
				noDefaultRoute:   true,
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
  dfmicro network attach --cluster first:gp1 --cluster second:gp2 --cluster third:gp1,gp2 --to backbone`,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "cluster",
				Usage:    "Cluster name with optional groups (name[:group1[,group2,...]]). Without group, cluster joins 'default'",
				Required: true,
				Validator: func(clusterGroups []string) error {
					seen := make(map[string]bool)
					for _, spec := range clusterGroups {
						name, _, err := parseClusterGroup(spec)
						if err != nil {
							return err
						}
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
			clusterGroups := cmd.StringSlice("cluster")
			networkName := cmd.String("to")
			namespace := cmd.String("namespace")

			clusterToGroups := make(map[string][]string)
			for _, spec := range clusterGroups {
				name, groups, err := parseClusterGroup(spec)
				if err != nil {
					return err
				}
				clusterToGroups[name] = groups
			}

			bridgeState, err := loadBridgeState(networkStateDir(rootconfig.ConfigDir()), networkName)
			if err != nil {
				return fmt.Errorf("failed to load bridge state for network %s: %w", networkName, err)
			}

			ipam, err := newIPAMManager(networkStateDir(rootconfig.ConfigDir()), networkName)
			if err != nil {
				return fmt.Errorf("failed to load IPAM state for network %s: %w", networkName, err)
			}

			ops := &multusOps{
				logger: logger,
				runner: runner,
				nad:    newNADManager(logger, runner, "oc"),
				ipam:   ipam,
			}

			if err := ops.attachClusters(ctx, bridgeState, clusterToGroups, networkName, namespace); err != nil {
				return err
			}
			return ipam.save(networkStateDir(rootconfig.ConfigDir()))
		},
	}
}

func detachCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "detach",
		Usage: "Detach clusters from a network",
		UsageText: `Detach one or more clusters from a bridge network.

Example:
  dfmicro network detach --cluster first:gp1 --cluster second:gp2 --from backbone`,
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:     "cluster",
				Usage:    "Cluster name with optional groups (name[:group1[,group2,...]]). Without group, detaches from 'default'",
				Required: true,
				Validator: func(clusterGroups []string) error {
					seen := make(map[string]bool)
					for _, spec := range clusterGroups {
						name, _, err := parseClusterGroup(spec)
						if err != nil {
							return err
						}
						if seen[name] {
							return fmt.Errorf("duplicate cluster name: %s", name)
						}
						seen[name] = true
					}
					return nil
				},
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
			clusterGroups := cmd.StringSlice("cluster")
			networkName := cmd.String("from")
			namespace := cmd.String("namespace")

			clusterToGroups := make(map[string][]string)
			for _, spec := range clusterGroups {
				name, groups, err := parseClusterGroup(spec)
				if err != nil {
					return err
				}
				clusterToGroups[name] = groups
			}

			ipam, err := newIPAMManager(networkStateDir(rootconfig.ConfigDir()), networkName)
			if err != nil {
				return fmt.Errorf("failed to load IPAM state for network %s: %w", networkName, err)
			}

			ops := &multusOps{
				logger: logger,
				runner: runner,
				nad:    newNADManager(logger, runner, "oc"),
				ipam:   ipam,
			}

			if err := ops.detachClusters(ctx, clusterToGroups, networkName, namespace); err != nil {
				return err
			}
			return ipam.save(networkStateDir(rootconfig.ConfigDir()))
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
			return mgr.delete(ctx, networkName, networkStateDir(rootconfig.ConfigDir()))
		},
	}
}

func configCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Show bridge and IPAM configuration",
		UsageText: `Show bridge and IPAM state for a network.

Example:
  dfmicro network config --name backbone`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "name",
				Usage:    "Network name",
				Required: true,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			networkName := cmd.String("name")
			stateDir := networkStateDir(rootconfig.ConfigDir())

			bridgeState, err := loadBridgeState(stateDir, networkName)
			if err != nil {
				return fmt.Errorf("failed to load bridge state: %w", err)
			}

			ipam, err := newIPAMManager(stateDir, networkName)
			if err != nil {
				return fmt.Errorf("failed to load IPAM state: %w", err)
			}

			config := map[string]any{
				"bridge": bridgeState,
				"ipam":   ipam,
			}

			data, err := json.MarshalIndent(config, "", "  ")
			if err != nil {
				return err
			}
			data = append(data, '\n')
			_, err = os.Stdout.Write(data)
			return err
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

func networkStateDir(baseDir string) string {
	return filepath.Join(baseDir, ",networks")
}

func parseClusterGroup(spec string) (string, []string, error) {
	parts := strings.Split(spec, ":")
	clusterName := parts[0]
	if clusterName == "" {
		return "", nil, fmt.Errorf("invalid cluster spec: empty cluster name")
	}

	var groups []string
	if len(parts) > 1 {
		groupStr := parts[1]
		for g := range strings.SplitSeq(groupStr, ",") {
			g = strings.TrimSpace(g)
			if g == "" {
				return "", nil, fmt.Errorf("invalid cluster spec: empty group name in %s", spec)
			}
			groups = append(groups, g)
		}
	} else {
		groups = []string{"default"}
	}
	return clusterName, groups, nil
}
