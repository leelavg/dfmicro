package network

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"dfmicro/internal/cluster"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func clusterContainers(ctx context.Context, runner execx.Runner, clusterName string) ([]string, error) {
	result, err := execx.RunPodmanCommand(ctx, runner, "ps", "-a", "--filter", "label=part-of="+clusterName, "--format=json")
	if err != nil {
		return nil, err
	}
	var containers []struct {
		Names []string `json:"Names"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &containers); err != nil {
		return nil, err
	}
	var names []string
	for _, c := range containers {
		names = append(names, c.Names...)
	}
	return names, nil
}

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
			},
			&cli.IntFlag{
				Name:  "segment-count",
				Usage: "Number of IPAM segments for clusters",
				Value: rootconfig.Load().BridgeSegmentCount,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			mgr := newBridgeManager(logger, runner)
			return mgr.create(ctx, BridgeConfig{
				Name:         cmd.String("name"),
				Subnet:       cmd.String("subnet"),
				SegmentCount: cmd.Int("segment-count"),
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
			mgr := newBridgeManager(logger, runner)
			clusterNames := cmd.StringSlice("cluster")
			networkName := cmd.String("to")
			namespace := cmd.String("namespace")

			subnet, err := mgr.getSubnet(ctx, networkName)
			if err != nil {
				return err
			}

			bridgeState, err := LoadBridgeState(bridgeStateDir(), networkName)
			if err != nil {
				return fmt.Errorf("failed to load bridge state for network %s: %w", networkName, err)
			}

			for _, clusterName := range clusterNames {
				kcPath, err := cluster.Kubeconfig(clusterName)
				if err != nil {
					return fmt.Errorf("failed to get kubeconfig for cluster %s: %w", clusterName, err)
				}

				containers, err := clusterContainers(ctx, runner, clusterName)
				if err != nil {
					return fmt.Errorf("list containers for cluster %s: %w", clusterName, err)
				}
				for _, c := range containers {
					logger.Info("connecting container to network", "container", c, "network", networkName)
					if _, err := execx.RunPodmanCommand(ctx, runner, "network", "connect", networkName, c); err != nil {
						return fmt.Errorf("connect container %s to network %s: %w", c, networkName, err)
					}
				}

				ipamAlloc, err := loadIPAMAllocation(networkName)
				if err != nil {
					return fmt.Errorf("failed to load IPAM state for network %s: %w", networkName, err)
				}

				segmentIndex, err := ipamAlloc.allocateForCluster(clusterName, bridgeState.SegmentCount)
				if err != nil {
					return fmt.Errorf("failed to allocate IPAM segment for cluster %s: %w", clusterName, err)
				}

				segmentSize := 256 / bridgeState.SegmentCount
				rangeStart, rangeEnd, err := computeIPAMRange(subnet, segmentIndex, segmentSize, bridgeState.SegmentCount)
				if err != nil {
					return fmt.Errorf("failed to compute IPAM range for cluster %s: %w", clusterName, err)
				}

				if err := ipamAlloc.save(); err != nil {
					return fmt.Errorf("failed to save IPAM state for cluster %s: %w", clusterName, err)
				}

				nadMgr := NewNADManager(logger, runner, "oc", kcPath)
				err = nadMgr.CreateNAD(ctx, NADConfig{
					Name:       networkName,
					Namespace:  namespace,
					Bridge:     networkName,
					Subnet:     subnet,
					RangeStart: rangeStart,
					RangeEnd:   rangeEnd,
				})
				if err != nil {
					return fmt.Errorf("failed to create NAD for cluster %s: %w", clusterName, err)
				}

				logger.Info("attached cluster to network", "cluster", clusterName, "network", networkName, "segment", segmentIndex)
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

			for _, clusterName := range clusterNames {
				kcPath, err := cluster.Kubeconfig(clusterName)
				if err != nil {
					return fmt.Errorf("failed to get kubeconfig for cluster %s: %w", clusterName, err)
				}

				containers, err := clusterContainers(ctx, runner, clusterName)
				if err != nil {
					return fmt.Errorf("list containers for cluster %s: %w", clusterName, err)
				}
				for _, c := range containers {
					logger.Info("disconnecting container from network", "container", c, "network", networkName)
					if _, err := execx.RunPodmanCommand(ctx, runner, "network", "disconnect", networkName, c); err != nil {
						logger.Warn("failed to disconnect container from network", "container", c, "network", networkName, "error", err)
					}
				}

				nadMgr := NewNADManager(logger, runner, "oc", kcPath)
				if err := nadMgr.DeleteNAD(ctx, networkName, namespace); err != nil {
					return fmt.Errorf("failed to delete NAD for cluster %s: %w", clusterName, err)
				}

				ipamAlloc, err := loadIPAMAllocation(networkName)
				if err != nil {
					logger.Warn("failed to load IPAM state for cleanup", "network", networkName, "error", err)
				} else {
					ipamAlloc.deallocateCluster(clusterName)
					ipamAlloc.NextIndex = len(ipamAlloc.Segments)
					if err := ipamAlloc.save(); err != nil {
						logger.Warn("failed to save IPAM state after deallocation", "cluster", clusterName, "error", err)
					}
				}

				logger.Info("detached cluster from network", "cluster", clusterName, "network", networkName)
			}

			return nil
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
			if err := mgr.delete(ctx, networkName); err != nil {
				return err
			}

			ipamPath := filepath.Join(bridgeStateDir(), fmt.Sprintf("ipam-%s.json", networkName))
			if err := os.Remove(ipamPath); err != nil && !os.IsNotExist(err) {
				logger.Warn("failed to delete IPAM state file", "path", ipamPath, "error", err)
			}

			bridgeStatePath := filepath.Join(bridgeStateDir(), fmt.Sprintf("bridge-%s.json", networkName))
			if err := os.Remove(bridgeStatePath); err != nil && !os.IsNotExist(err) {
				logger.Warn("failed to delete bridge state file", "path", bridgeStatePath, "error", err)
			}

			return nil
		},
	}
}
