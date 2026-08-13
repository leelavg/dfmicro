package network

import (
	"context"
	"fmt"
	"log/slog"

	"dfmicro/internal/cluster"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

type netOps struct {
	logger *slog.Logger
	runner execx.Runner
	nad    *nadManager
	ipam   *ipamManager
}

func (o *netOps) attach(ctx context.Context, state *bridgeState, clusterName, networkName, namespace string) error {
	kcPath, err := cluster.Kubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig for cluster %s: %w", clusterName, err)
	}

	containers, err := support.AllClusterContainers(ctx, o.runner, clusterName)
	if err != nil {
		return fmt.Errorf("list containers for cluster %s: %w", clusterName, err)
	}
	for _, c := range containers {
		o.logger.Info("connecting container to network", "container", c, "network", networkName)
		if _, err := execx.RunPodmanCommand(ctx, o.runner, "network", "connect", networkName, c); err != nil {
			return fmt.Errorf("connect container %s to network %s: %w", c, networkName, err)
		}
	}

	segmentIndex, err := o.ipam.allocateForCluster(clusterName, state.SegmentCount)
	if err != nil {
		return fmt.Errorf("failed to allocate IPAM segment for cluster %s: %w", clusterName, err)
	}

	rangeStart, rangeEnd, err := computeIPAMRange(state.Subnet, segmentIndex, state.SegmentCount)
	if err != nil {
		return fmt.Errorf("failed to compute IPAM range for cluster %s: %w", clusterName, err)
	}

	if err := o.nad.create(ctx, nadConfig{
		name:       networkName,
		namespace:  namespace,
		kubeconfig: kcPath,
		bridge:     networkName,
		subnet:     state.Subnet,
		rangeStart: rangeStart,
		rangeEnd:   rangeEnd,
	}); err != nil {
		return fmt.Errorf("failed to create NAD for cluster %s: %w", clusterName, err)
	}

	o.logger.Info("attached cluster to network", "cluster", clusterName, "network", networkName, "segment", segmentIndex)
	return nil
}

func (o *netOps) detach(ctx context.Context, clusterName, networkName, namespace string) error {
	kcPath, err := cluster.Kubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig for cluster %s: %w", clusterName, err)
	}

	containers, err := support.AllClusterContainers(ctx, o.runner, clusterName)
	if err != nil {
		return fmt.Errorf("list containers for cluster %s: %w", clusterName, err)
	}
	for _, c := range containers {
		o.logger.Info("disconnecting container from network", "container", c, "network", networkName)
		if _, err := execx.RunPodmanCommand(ctx, o.runner, "network", "disconnect", networkName, c); err != nil {
			o.logger.Warn("failed to disconnect container from network", "container", c, "network", networkName, "error", err)
		}
	}

	if err := o.nad.delete(ctx, networkName, namespace, kcPath); err != nil {
		return fmt.Errorf("failed to delete NAD for cluster %s: %w", clusterName, err)
	}

	o.ipam.deallocateCluster(clusterName)
	o.logger.Info("detached cluster from network", "cluster", clusterName, "network", networkName)
	return nil
}
