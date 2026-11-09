package odr

import (
	"context"
	"embed"
	"fmt"

	"dfmicro/internal/addon/ocp"
	"dfmicro/internal/support"
)

//go:embed assets/*.yaml assets/velero-crds/*.yaml shims/crd/*.yaml shims/rbac/*.yaml shims/velero/crd/*.yaml shims/volsync/crd/*.yaml shims/volsync/cr/*.yaml
var odrFS embed.FS

func (o *odr) install(ctx context.Context, catalogImage, channel, version string) error {
	o.logger.Info("creating ODR shim CRDs if missing", "cluster", o.clusterName)
	if err := support.ApplyDirIfMissing(ctx, o.runner, support.KubeClient, o.kubeconfig, odrFS, "shims/crd"); err != nil {
		return err
	}
	o.logger.Info("creating Velero shim CRDs if missing", "cluster", o.clusterName)
	if err := support.ApplyDirIfMissing(ctx, o.runner, support.KubeClient, o.kubeconfig, odrFS, "shims/velero/crd"); err != nil {
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

	if err := o.applyTrustedCABundle(ctx, o.clusterName, "openshift-operators", "odr-hub-operator", "ramen-hub-operator"); err != nil {
		return err
	}
	return nil
}
