package odr

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"dfmicro/internal/addon/ocp"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

//go:embed shims/crd/*.yaml shims/rbac/*.yaml
var odrFS embed.FS

func (o *odr) install(ctx context.Context, catalogImage, channel, version string) error {
	o.logger.Info("applying ODR shim CRDs", "cluster", o.clusterName)
	if err := support.ApplyDir(ctx, o.runner, support.KubeClient, o.kubeconfig, odrFS, "shims/crd"); err != nil {
		return err
	}
	o.logger.Info("applying ODR shim RBAC", "cluster", o.clusterName)
	if err := support.ApplyDir(ctx, o.runner, support.KubeClient, o.kubeconfig, odrFS, "shims/rbac"); err != nil {
		return err
	}

	clusterVersion := ocp.NewClusterVersion(o.runner)
	o.logger.Info("applying ClusterVersion", "cluster", o.clusterName, "version", version)
	if err := clusterVersion.ApplyCRD(ctx, o.kubeconfig); err != nil {
		return err
	}
	if err := clusterVersion.Apply(ctx, o.kubeconfig, channel); err != nil {
		return err
	}
	if err := clusterVersion.PatchStatus(ctx, o.kubeconfig, version); err != nil {
		return err
	}

	o.logger.Info("applying ODF catalog source", "cluster", o.clusterName)
	catalogSource, err := support.Render(catalogSourceTmpl, map[string]string{"CatalogImage": catalogImage})
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, o.kubeconfig, catalogSource); err != nil {
		return fmt.Errorf("apply ODF catalog source: %w", err)
	}

	o.logger.Info("applying ODF Multicluster Orchestrator subscription", "cluster", o.clusterName)
	subscription, err := support.Render(subscriptionTmpl, map[string]string{"Channel": channel})
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, o.kubeconfig, subscription); err != nil {
		return fmt.Errorf("apply ODF Multicluster Orchestrator subscription: %w", err)
	}

	result, err := support.RunPodmanPrivileged(ctx, o.runner,
		"exec", rootconfig.NodeName(o.clusterName, 0),
		"cat", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	)
	if err != nil {
		return fmt.Errorf("read cluster TLS CA bundle: %w", err)
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return fmt.Errorf("cluster TLS CA bundle is empty")
	}
	caBundle, err := json.Marshal(result.Stdout)
	if err != nil {
		return fmt.Errorf("encode cluster TLS CA bundle: %w", err)
	}
	configMap, err := support.Render(trustedCABundleTmpl, map[string]string{"CABundle": string(caBundle)})
	if err != nil {
		return err
	}
	o.logger.Info("applying trusted CA bundle", "cluster", o.clusterName)
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, o.kubeconfig, configMap); err != nil {
		return fmt.Errorf("apply trusted CA bundle: %w", err)
	}
	return nil
}
