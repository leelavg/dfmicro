package odr

import (
	"context"
	"fmt"

	"dfmicro/internal/addon/ocp"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

var veleroCRDs = []string{
	"backups.velero.io",
	"backupstoragelocations.velero.io",
	"restores.velero.io",
}

func (o *odr) installVelero(ctx context.Context, clusterName string) error {
	kubeconfig, err := rootconfig.Kubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("load kubeconfig for Velero cluster %q: %w", clusterName, err)
	}

	o.logger.Info("applying Velero CRD shims if missing", "cluster", clusterName)
	if err := support.ApplyDirIfMissing(ctx, o.runner, support.KubeClient, kubeconfig, odrFS, "shims/velero/crd"); err != nil {
		return fmt.Errorf("apply Velero CRD shims to cluster %q: %w", clusterName, err)
	}

	o.logger.Info("applying Velero CRDs", "cluster", clusterName, "version", veleroVersion)
	if err := support.ApplyDir(ctx, o.runner, support.KubeClient, kubeconfig, odrFS, "assets/velero-crds"); err != nil {
		return fmt.Errorf("apply Velero CRDs to cluster %q: %w", clusterName, err)
	}
	for _, crd := range veleroCRDs {
		if err := ocp.WaitForCRD(ctx, o.runner, support.KubeClient, kubeconfig, crd); err != nil {
			return fmt.Errorf("wait for Velero CRD %s in cluster %q: %w", crd, clusterName, err)
		}
	}

	resources, err := odrFS.ReadFile("assets/velero.yaml")
	if err != nil {
		return err
	}
	o.logger.Info("applying Velero", "cluster", clusterName, "version", veleroVersion, "awsPlugin", veleroAWSPluginVersion)
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, kubeconfig, string(resources)); err != nil {
		return fmt.Errorf("apply Velero to cluster %q: %w", clusterName, err)
	}
	return nil
}
