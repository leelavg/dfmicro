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
	*networkOps

	logger *slog.Logger
	runner execx.Runner
	nad    *nadManager
	ipam   *ipamManager
}

type networkOps struct {
	logger *slog.Logger
	runner execx.Runner
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

func (o *networkOps) connect(ctx context.Context, networkName, containerName string) error {
	if support.ContainerConnectedToNetwork(ctx, o.runner, networkName, containerName) {
		o.logger.Info("container already connected to network", "container", containerName, "network", networkName)
		return nil
	}

	o.logger.Info("connecting container to network", "container", containerName, "network", networkName)
	if _, err := support.RunPodmanPrivileged(ctx, o.runner, "network", "connect", networkName, containerName); err != nil {
		return fmt.Errorf("connect container %s to network %s: %w", containerName, networkName, err)
	}
	return nil
}

func (o *networkOps) disconnect(ctx context.Context, networkName, containerName string) error {
	if !support.ContainerConnectedToNetwork(ctx, o.runner, networkName, containerName) {
		o.logger.Info("container not connected to network", "container", containerName, "network", networkName)
		return nil
	}

	o.logger.Info("disconnecting container from network", "container", containerName, "network", networkName)
	if _, err := support.RunPodmanPrivileged(ctx, o.runner, "network", "disconnect", networkName, containerName); err != nil {
		return fmt.Errorf("disconnect container %s from network %s: %w", containerName, networkName, err)
	}
	return nil
}

func (o *networkOps) connectClusters(ctx context.Context, clusterNames []string, networkName string) error {
	for _, clusterName := range clusterNames {
		containers, err := support.AllClusterContainers(ctx, o.runner, clusterName)
		if err != nil {
			return fmt.Errorf("list containers for cluster %s: %w", clusterName, err)
		}
		for _, containerName := range containers {
			if err := o.connect(ctx, networkName, containerName); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *networkOps) disconnectClusters(ctx context.Context, clusterNames []string, networkName string) error {
	for _, clusterName := range clusterNames {
		containers, err := support.AllClusterContainers(ctx, o.runner, clusterName)
		if err != nil {
			return fmt.Errorf("list containers for cluster %s: %w", clusterName, err)
		}
		for _, containerName := range containers {
			if err := o.disconnect(ctx, networkName, containerName); err != nil {
				return err
			}
		}
	}
	return nil
}

func (o *multusOps) attachClusters(ctx context.Context, state *bridgeState, clusterToGroups map[string][]string, networkName, namespace string) error {

	groupToClusters := make(map[string][]string)
	for clusterName, groups := range clusterToGroups {
		for _, group := range groups {
			groupToClusters[group] = append(groupToClusters[group], clusterName)
		}
	}

	for groupName := range groupToClusters {
		clusterNames := groupToClusters[groupName]

		group, err := o.ipam.addGroup(groupName, state.Subnet, state.GroupCount, state.ReservePerGroup, state.ClustersPerGroup)
		if err != nil {
			return fmt.Errorf("failed to allocate group %s: %w", groupName, err)
		}

		var clusterRange *clusterRange
		var clusterEth string
		for _, clusterName := range clusterNames {
			clusterRange, err = group.addCluster(clusterName, state.ClustersPerGroup)
			if err != nil {
				return fmt.Errorf("failed to add cluster %s to group %s: %w", clusterName, groupName, err)
			}
			containers, err := support.AllClusterContainers(ctx, o.runner, clusterName)
			if err != nil {
				return fmt.Errorf("list containers for cluster %s: %w", clusterName, err)
			}
			for _, c := range containers {
				if err := o.networkOps.connect(ctx, networkName, c); err != nil {
					return err
				}

				containerEth, err := support.GetContainerEth(ctx, o.runner, networkName, c)
				if err != nil {
					o.logger.Warn("failed to get container eth", "container", c, "error", err)
					continue
				}
				if clusterEth == "" {
					clusterEth = containerEth
				} else if clusterEth != containerEth {
					return fmt.Errorf(
						"cluster %s has inconsistent eth interfaces: %s vs %s (container %s is part of multiple networks)",
						clusterName, clusterEth, containerEth, c,
					)
				}
				devName := fmt.Sprintf("%s.%d", clusterEth, group.VlanID)
				if !support.VlanInterfaceExists(ctx, o.runner, c, devName) {
					if _, err := support.RunPodmanPrivileged(ctx, o.runner, "exec", c, "ip", "link", "add",
						"link", clusterEth,
						"name", devName, "up",
						"type", "vlan",
						"id", fmt.Sprint(group.VlanID),
					); err != nil {
						o.logger.Warn("failed to create vlan subif", "container", c, "subif", devName, "error", err)
						continue
					}
				} else {
					o.logger.Info("vlan interface already exists", "container", c, "subif", devName)
				}
			}
			kcPath, err := cluster.Kubeconfig(clusterName)
			if err != nil {
				return fmt.Errorf("failed to get kubeconfig for cluster %s: %w", clusterName, err)
			}

			nadName := fmt.Sprintf("%s-%s", networkName, groupName)
			if err := o.nad.create(ctx, nadConfig{
				name:       nadName,
				namespace:  namespace,
				kubeconfig: kcPath,
				bridge:     networkName,
				subnet:     group.Subnet,
				rangeStart: clusterRange.RangeStart,
				rangeEnd:   clusterRange.RangeEnd,
				master:     fmt.Sprintf("%s.%d", clusterEth, group.VlanID),
				ipamType:   map[bool]string{true: "whereabouts", false: "host-local"}[o.ipam.MultiNode],
			}); err != nil {
				return fmt.Errorf("failed to create NAD %s for cluster %s: %w", nadName, clusterName, err)
			}
			o.logger.Info("created NAD for cluster in group", "cluster", clusterName, "group", groupName, "nad", nadName, "vlan", group.VlanID)
			if o.ipam.MultiNode {
				if err := cluster.SetMultiNodeNetwork(clusterName, networkName, true); err != nil {
					return fmt.Errorf("save multi-node state for cluster %s: %w", clusterName, err)
				}
				if err := o.labelClusterNodes(ctx, clusterName, true); err != nil {
					return err
				}
			}
		}

	}
	return nil
}

func (o *multusOps) detachClusters(ctx context.Context, clusterToGroups map[string][]string, networkName, namespace string) error {

	groupToClusters := make(map[string][]string)
	for clusterName, groups := range clusterToGroups {
		for _, group := range groups {
			groupToClusters[group] = append(groupToClusters[group], clusterName)
		}
	}

	for groupName := range groupToClusters {
		clusterNames := groupToClusters[groupName]

		groupIdx, group, err := o.ipam.getGroup(groupName)
		if err != nil {
			o.logger.Warn(err.Error())
			continue
		}

		for _, clusterName := range clusterNames {
			if err := group.removeCluster(clusterName); err != nil {
				return fmt.Errorf("failed to remove cluster %s from group %s: %w", clusterName, groupName, err)
			}
			containers, err := support.AllClusterContainers(ctx, o.runner, clusterName)
			if err != nil {
				return fmt.Errorf("list containers for cluster %s: %w", clusterName, err)
			}
			for _, c := range containers {
				if err := o.networkOps.disconnect(ctx, networkName, c); err != nil {
					return err
				}
			}
			kcPath, err := cluster.Kubeconfig(clusterName)
			if err != nil {
				return fmt.Errorf("failed to get kubeconfig for cluster %s: %w", clusterName, err)
			}

			nadName := fmt.Sprintf("%s-%s", networkName, groupName)
			if err := o.nad.delete(ctx, nadName, namespace, kcPath); err != nil {
				return fmt.Errorf("failed to create NAD %s for cluster %s: %w", nadName, clusterName, err)
			}
			if o.ipam.MultiNode && !o.ipam.hasCluster(clusterName) {
				if err := cluster.SetMultiNodeNetwork(clusterName, networkName, false); err != nil {
					return fmt.Errorf("save multi-node state for cluster %s: %w", clusterName, err)
				}
				multiNode, err := cluster.MultiNodeEnabled(clusterName)
				if err != nil {
					return fmt.Errorf("read multi-node state for cluster %s: %w", clusterName, err)
				}
				if err := o.labelClusterNodes(ctx, clusterName, multiNode); err != nil {
					return err
				}
			}
			o.logger.Info("deleted NAD for cluster in group", "cluster", clusterName, "group", groupName, "nad", nadName, "vlan", group.VlanID)
		}

		if err := o.ipam.removeGroup(groupIdx); err != nil {
			return err
		}
	}
	return nil
}

func (o *multusOps) labelClusterNodes(ctx context.Context, clusterName string, enabled bool) error {
	kubeconfig, err := cluster.Kubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("get kubeconfig for cluster %s: %w", clusterName, err)
	}
	label := "dfmicro.io/whereabouts-"
	if enabled {
		label = "dfmicro.io/whereabouts=enabled"
	}
	if _, err := o.runner.Run(ctx, "kubectl", "label", "nodes", "--all", label, "--overwrite", "--kubeconfig", kubeconfig); err != nil {
		return fmt.Errorf("label nodes for cluster %s: %w", clusterName, err)
	}
	return nil
}

func (o *peerOps) run(
	ctx context.Context,
	dstNames []string,
	commandBuilder func(networkByClusterName map[string]networkInfo, srcName, containerName string, dstNames []string) string,
) error {
	networkByClusterName := make(map[string]networkInfo)
	clusterContainers := make(map[string][]string)

	for _, name := range dstNames {
		containers, err := support.AllClusterContainers(ctx, o.runner, name)
		if err != nil || len(containers) == 0 {
			return fmt.Errorf("failed to find containers for cluster %s: %w", name, err)
		}
		clusterContainers[name] = containers

		gatewayNode := name + "-1"
		cfg, err := cluster.ReadClusterConfig(name)
		if err != nil {
			return fmt.Errorf("failed to read config for cluster %s: %w", name, err)
		}
		nodeIP, err := support.GetContainerIP(ctx, o.runner, cfg.BridgeName, gatewayNode)
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
			if _, err := support.RunPodmanPrivileged(ctx, o.runner, "exec", container, "bash", "-c", commandBuilder(networkByClusterName, srcName, container, dstNames)); err != nil {
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
		func(networkByClusterName map[string]networkInfo, srcName, containerName string, dstNames []string) string {
			var commands strings.Builder
			commands.WriteString("set -e\n")
			srcNetwork := networkByClusterName[srcName]
			for dstName, dstNetwork := range networkByClusterName {
				if srcName != dstName {
					nextHop := srcNetwork.nodeIP
					if containerName == srcName+"-1" {
						nextHop = dstNetwork.nodeIP
					}
					o.logger.Info("establishing peering", "from", srcName, "to", dstName, "container", containerName, "nextHop", nextHop)
					commands.WriteString(fmt.Sprintf("ip route replace %s via %s\n", dstNetwork.clusterCIDR, nextHop))
					commands.WriteString(fmt.Sprintf("ip route replace %s via %s\n", dstNetwork.serviceCIDR, nextHop))
					commands.WriteString(fmt.Sprintf("iptables -t nat -I KIND-MASQ-AGENT 1 -d %s -j RETURN\n", dstNetwork.clusterCIDR))
					commands.WriteString(fmt.Sprintf("iptables -t nat -I KIND-MASQ-AGENT 1 -d %s -j RETURN\n", dstNetwork.serviceCIDR))
					commands.WriteString(fmt.Sprintf("firewall-cmd --zone=trusted --add-source=%s\n", dstNetwork.clusterCIDR))
					commands.WriteString(fmt.Sprintf("firewall-cmd --zone=trusted --add-source=%s\n", dstNetwork.serviceCIDR))
					commands.WriteString(fmt.Sprintf("firewall-cmd --zone=trusted --add-source=%s/32\n", dstNetwork.nodeIP))
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
		func(networkByClusterName map[string]networkInfo, srcName, containerName string, dstNames []string) string {
			var commands strings.Builder
			commands.WriteString("set -e\n")
			srcNetwork := networkByClusterName[srcName]
			for dstName, dstNetwork := range networkByClusterName {
				if srcName == dstName {
					continue
				}
				nextHop := srcNetwork.nodeIP
				if containerName == srcName+"-1" {
					nextHop = dstNetwork.nodeIP
				}
				o.logger.Info("removing peering", "from", srcName, "to", dstName, "container", containerName, "nextHop", nextHop)
				commands.WriteString(fmt.Sprintf("ip route del %s\n", dstNetwork.clusterCIDR))
				commands.WriteString(fmt.Sprintf("ip route del %s\n", dstNetwork.serviceCIDR))
				commands.WriteString(fmt.Sprintf("iptables -t nat -D KIND-MASQ-AGENT -d %s -j RETURN\n", dstNetwork.clusterCIDR))
				commands.WriteString(fmt.Sprintf("iptables -t nat -D KIND-MASQ-AGENT -d %s -j RETURN\n", dstNetwork.serviceCIDR))
				commands.WriteString(fmt.Sprintf("firewall-cmd --zone=trusted --remove-source=%s\n", dstNetwork.clusterCIDR))
				commands.WriteString(fmt.Sprintf("firewall-cmd --zone=trusted --remove-source=%s\n", dstNetwork.serviceCIDR))
				commands.WriteString(fmt.Sprintf("firewall-cmd --zone=trusted --remove-source=%s/32\n", dstNetwork.nodeIP))
			}
			return commands.String()
		},
	); err != nil {
		return err
	}
	o.logger.Info("clusters unpeered successfully", "clusters", clusterNames)
	return nil
}
