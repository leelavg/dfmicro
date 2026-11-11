package odr

import (
	"context"
	"fmt"

	"dfmicro/internal/addon/ocp"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

func (o *odr) installVolSync(ctx context.Context, clusterName string) error {
	kubeconfig, err := rootconfig.Kubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("load kubeconfig for VolSync cluster %q: %w", clusterName, err)
	}

	o.logger.Info("applying VolSync shim CRDs if missing", "cluster", clusterName)
	if err := support.ApplyDirIfMissing(ctx, o.runner, support.KubeClient, kubeconfig, odrFS, "shims/volsync/crd"); err != nil {
		return fmt.Errorf("apply VolSync shim CRDs to cluster %q: %w", clusterName, err)
	}
	if err := ocp.WaitForCRD(ctx, o.runner, support.KubeClient, kubeconfig, "apiservers.config.openshift.io"); err != nil {
		return fmt.Errorf("wait for VolSync APIServer CRD in cluster %q: %w", clusterName, err)
	}

	apiserver, err := odrFS.ReadFile("shims/volsync/cr/02-apiserver.yaml")
	if err != nil {
		return err
	}
	volsync, err := odrFS.ReadFile("assets/volsync.yaml")
	if err != nil {
		return err
	}
	manifests := string(apiserver) + "\n---\n" + string(volsync)
	o.logger.Info("applying VolSync manifests to managed cluster", "cluster", clusterName)
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, kubeconfig, manifests); err != nil {
		return fmt.Errorf("apply VolSync manifests to cluster %q: %w", clusterName, err)
	}
	return nil
}
