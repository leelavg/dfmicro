package network

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"dfmicro/internal/cluster"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

type multusOps struct {
	logger *slog.Logger
	runner execx.Runner
	nad    *nadManager
	ipam   *support.IPAMManager
}

type peerOps struct {
	logger *slog.Logger
	runner execx.Runner
}

type networkInfo struct {
	nodeIP      string
	clusterCIDR string
	serviceCIDR string
}

func (o *multusOps) attach(ctx context.Context, state *support.BridgeState, clusterName, networkName, namespace string) error {
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

	segmentIndex, err := o.ipam.AllocateForCluster(clusterName, state.SegmentCount)
	if err != nil {
		return fmt.Errorf("failed to allocate IPAM segment for cluster %s: %w", clusterName, err)
	}

	rangeStart, rangeEnd, err := support.ComputeIPAMRange(state.Subnet, segmentIndex, state.SegmentCount, state.ReservePerSegment)
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

func (o *multusOps) detach(ctx context.Context, clusterName, networkName, namespace string) error {
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

	o.ipam.DeallocateCluster(clusterName)
	o.logger.Info("detached cluster from network", "cluster", clusterName, "network", networkName)
	return nil
}

func (o *peerOps) run(
	ctx context.Context,
	dstNames []string,
	commandBuilder func(networkByClusterName map[string]networkInfo, srcName string, dstNames []string) string,
) error {
	networkByClusterName := make(map[string]networkInfo)
	clusterContainers := make(map[string][]string)

	for _, name := range dstNames {
		containers, err := support.AllClusterContainers(ctx, o.runner, name)
		if err != nil || len(containers) == 0 {
			return fmt.Errorf("failed to find containers for cluster %s: %w", name, err)
		}
		clusterContainers[name] = containers

		gatewayNode := containers[0]
		nodeIP, err := o.getNodeIP(ctx, gatewayNode)
		if err != nil {
			return fmt.Errorf("failed to get node IP for cluster %s: %w", name, err)
		}

		cidrs, err := cluster.GetCIDRs(name)
		if err != nil {
			return fmt.Errorf("failed to get CIDRs for cluster %s: %w", name, err)
		}

		networkByClusterName[name] = networkInfo{
			nodeIP:      nodeIP,
			clusterCIDR: cidrs.Cluster,
			serviceCIDR: cidrs.Service,
		}
		o.logger.Info("resolved cluster gateway node", "cluster", name, "node", gatewayNode, "nodeIP", nodeIP, "clusterCIDR", cidrs.Cluster, "serviceCIDR", cidrs.Service)
	}

	seen := make(map[string][]string)
	for name, network := range networkByClusterName {
		seen[network.clusterCIDR] = append(seen[network.clusterCIDR], name)
		seen[network.serviceCIDR] = append(seen[network.serviceCIDR], name)
	}
	for cidr, names := range seen {
		if len(names) > 1 {
			return fmt.Errorf("CIDR overlap: %s used by clusters: %v", cidr, names)
		}
	}

	for srcName, containers := range clusterContainers {
		for _, container := range containers {
			if _, err := execx.RunPodmanCommand(ctx, o.runner, "exec", container, "bash", "-c", commandBuilder(networkByClusterName, srcName, dstNames)); err != nil {
				return fmt.Errorf("failed to configure on container %s: %w", container, err)
			}
		}
	}

	return nil
}

func (o *peerOps) peer(ctx context.Context, clusterNames []string) error {
	if err := o.run(
		ctx,
		clusterNames,
		func(networkByClusterName map[string]networkInfo, srcName string, dstNames []string) string {
			var commands strings.Builder
			for dstName, dstNetwork := range networkByClusterName {
				if srcName != dstName {
					o.logger.Info("establishing peering", "from", srcName, "to", dstName, "nextHop", dstNetwork.nodeIP)
					commands.WriteString(fmt.Sprintf("ip route replace %s via %s\n", dstNetwork.clusterCIDR, dstNetwork.nodeIP))
					commands.WriteString(fmt.Sprintf("ip route replace %s via %s\n", dstNetwork.serviceCIDR, dstNetwork.nodeIP))
					commands.WriteString(fmt.Sprintf("iptables -t nat -I KIND-MASQ-AGENT 1 -d %s -j RETURN\n", dstNetwork.clusterCIDR))
					commands.WriteString(fmt.Sprintf("iptables -t nat -I KIND-MASQ-AGENT 1 -d %s -j RETURN\n", dstNetwork.serviceCIDR))
				}
			}
			return commands.String()
		},
	); err != nil {
		return err
	}
	o.logger.Info("clusters peered successfully, run again on node restart", "clusters", clusterNames)
	return nil
}

func (o *peerOps) unpeer(ctx context.Context, clusterNames []string) error {
	if err := o.run(
		ctx,
		clusterNames,
		func(networkByClusterName map[string]networkInfo, srcName string, dstNames []string) string {
			var commands strings.Builder
			for dstName, dstNetwork := range networkByClusterName {
				if srcName == dstName {
					continue
				}
				o.logger.Info("removing peering", "from", srcName, "to", dstName)
				commands.WriteString(fmt.Sprintf("ip route del %s 2>/dev/null || true\n", dstNetwork.clusterCIDR))
				commands.WriteString(fmt.Sprintf("ip route del %s 2>/dev/null || true\n", dstNetwork.serviceCIDR))
				commands.WriteString(fmt.Sprintf("iptables -t nat -D KIND-MASQ-AGENT -d %s -j RETURN 2>/dev/null || true\n", dstNetwork.clusterCIDR))
				commands.WriteString(fmt.Sprintf("iptables -t nat -D KIND-MASQ-AGENT -d %s -j RETURN 2>/dev/null || true\n", dstNetwork.serviceCIDR))
			}
			return commands.String()
		},
	); err != nil {
		return err
	}
	o.logger.Info("clusters unpeered successfully", "clusters", clusterNames)
	return nil
}

func (o *peerOps) getNodeIP(ctx context.Context, container string) (string, error) {
	result, err := execx.RunPodmanCommand(ctx, o.runner, "inspect", container, "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}")
	if err != nil {
		return "", fmt.Errorf("failed to get node IP: %w", err)
	}

	nodeIP := strings.TrimSpace(result.Stdout)
	if nodeIP == "" {
		return "", fmt.Errorf("could not determine node IP for container %s", container)
	}

	return nodeIP, nil
}
