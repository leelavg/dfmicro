package locals3

import (
	"context"
	"fmt"
	"strings"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

func (m *manager) configure(ctx context.Context, attachTo, detachFrom []string) error {
	for _, cluster := range attachTo {
		if err := m.attach(ctx, strings.TrimSpace(cluster)); err != nil {
			return err
		}
	}
	for _, cluster := range detachFrom {
		if err := m.detach(ctx, strings.TrimSpace(cluster)); err != nil {
			return err
		}
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
	if err := support.ApplyYAML(ctx, m.runner, support.KubeClient, clusterConfig.Kubeconfig, manifest); err != nil {
		return fmt.Errorf("publish local-s3 in cluster %q: %w", cluster, err)
	}
	m.logger.Info("created s3.default.svc Service and EndpointSlice", "cluster", cluster, "address", address)
	return nil
}

func (m *manager) detach(ctx context.Context, cluster string) error {
	kubeconfig, err := rootconfig.Kubeconfig(cluster)
	if err != nil {
		return fmt.Errorf("read kubeconfig for cluster %q: %w", cluster, err)
	}
	if _, err := m.runner.Run(ctx, support.KubeClient, "delete", "services,endpointslices", "s3",
		"-n", "default", "--ignore-not-found", "--kubeconfig", kubeconfig); err != nil {
		return fmt.Errorf("remove local-s3 Service from cluster %q: %w", cluster, err)
	}
	clusters, err := loadState()
	if err != nil {
		return err
	}
	return saveState(removeCluster(clusters, cluster))
}
