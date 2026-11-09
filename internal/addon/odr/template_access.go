package odr

import (
	"context"
	"fmt"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

func (o *odr) applyTemplateAccess(ctx context.Context, clusterName string) error {
	kubeconfig, err := rootconfig.Kubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("load kubeconfig for managed cluster %q: %w", clusterName, err)
	}

	manifest, err := odrFS.ReadFile("assets/mco-template-access.yaml")
	if err != nil {
		return err
	}

	o.logger.Info("applying OCM Template access", "cluster", clusterName)
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, kubeconfig, string(manifest)); err != nil {
		return fmt.Errorf("apply OCM Template access to cluster %q: %w", clusterName, err)
	}
	return nil
}
