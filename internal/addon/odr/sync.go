package odr

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"dfmicro/internal/addon/ocp"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

const (
	csiAddonsNodeCRD = "csiaddonsnodes.csiaddons.openshift.io"
	drClusterCRD     = "drclusters.ramendr.openshift.io"
)

func (o *odr) configureSyncCIDRs(ctx context.Context, managedClusters []string) error {
	if err := ocp.WaitForCRD(ctx, o.runner, support.KubeClient, o.kubeconfig, drClusterCRD); err != nil {
		return err
	}

	for _, clusterName := range managedClusters {
		kubeconfig, err := rootconfig.Kubeconfig(clusterName)
		if err != nil {
			return fmt.Errorf("load kubeconfig for cluster %q: %w", clusterName, err)
		}
		if err := ocp.WaitForCRD(ctx, o.runner, support.KubeClient, kubeconfig, csiAddonsNodeCRD); err != nil {
			return err
		}

		cidrs, err := o.networkFenceClientCIDRs(ctx, clusterName, kubeconfig)
		if err != nil {
			return err
		}
		if err := o.waitForDRCluster(ctx, clusterName); err != nil {
			return err
		}
		if err := o.patchDRClusterCIDRs(ctx, clusterName, cidrs); err != nil {
			return err
		}
	}
	return nil
}

func (o *odr) networkFenceClientCIDRs(ctx context.Context, clusterName, kubeconfig string) ([]string, error) {
	var cidrs []string
	err := support.Poll(ctx, 2*time.Second, 10*time.Minute, func(ctx context.Context) (bool, error) {
		result, err := o.runner.Run(ctx, support.KubeClient,
			"get", csiAddonsNodeCRD, "-A", "-o", "json",
			"--kubeconfig", kubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("get CSIAddonsNodes in cluster %q: %w", clusterName, err)
		}

		var nodes struct {
			Items []struct {
				Status struct {
					NetworkFenceClientStatus []struct {
						ClientDetails []struct {
							CIDRs []string `json:"cidrs"`
						} `json:"ClientDetails"`
					} `json:"networkFenceClientStatus"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(result.Stdout), &nodes); err != nil {
			return false, fmt.Errorf("decode CSIAddonsNodes in cluster %q: %w", clusterName, err)
		}

		unique := make(map[string]struct{})
		for _, node := range nodes.Items {
			for _, fenceClient := range node.Status.NetworkFenceClientStatus {
				for _, client := range fenceClient.ClientDetails {
					for _, cidr := range client.CIDRs {
						if _, _, err := net.ParseCIDR(cidr); err != nil {
							return false, fmt.Errorf("invalid network fence client CIDR %q in cluster %q: %w", cidr, clusterName, err)
						}
						unique[cidr] = struct{}{}
					}
				}
			}
		}
		cidrs = make([]string, 0, len(unique))
		for cidr := range unique {
			cidrs = append(cidrs, cidr)
		}
		slices.Sort(cidrs)
		return len(cidrs) > 0, nil
	})
	if err != nil {
		return nil, fmt.Errorf("wait for network fence client CIDRs in cluster %q: %w", clusterName, err)
	}
	o.logger.Info("found network fence client CIDRs", "cluster", clusterName, "cidrs", cidrs)
	return cidrs, nil
}

func (o *odr) waitForDRCluster(ctx context.Context, clusterName string) error {
	return support.Poll(ctx, 2*time.Second, 10*time.Minute, func(ctx context.Context) (bool, error) {
		result, err := o.runner.Run(ctx, support.KubeClient,
			"get", "drcluster", clusterName, "--ignore-not-found",
			"-o=jsonpath={.metadata.name}", "--kubeconfig", o.kubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("get DRCluster %q: %w", clusterName, err)
		}
		return strings.TrimSpace(result.Stdout) == clusterName, nil
	})
}

func (o *odr) patchDRClusterCIDRs(ctx context.Context, clusterName string, cidrs []string) error {
	patch, err := json.Marshal(map[string]any{
		"spec": map[string][]string{"cidrs": cidrs},
	})
	if err != nil {
		return fmt.Errorf("encode DRCluster %q CIDRs: %w", clusterName, err)
	}
	o.logger.Info("setting sync DRCluster CIDRs", "cluster", clusterName, "cidrs", cidrs)
	if _, err := o.runner.Run(ctx, support.KubeClient,
		"patch", "drcluster", clusterName, "--type=merge", "--patch", string(patch),
		"--kubeconfig", o.kubeconfig,
	); err != nil {
		return fmt.Errorf("set sync DRCluster CIDRs for cluster %q: %w", clusterName, err)
	}
	return nil
}
