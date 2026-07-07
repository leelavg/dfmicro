package odf

import (
	"context"
	"fmt"

	"dfmicro/internal/support"
)

func (o *odf) Configure(ctx context.Context) error {
	o.logger.Info("checking StorageCluster CRD presence")
	args := []string{"get", "crd", "storageclusters.ocs.openshift.io"}
	if o.kubeconfig != "" {
		args = append(args, "--kubeconfig", o.kubeconfig)
	}
	if _, err := o.runner.Run(ctx, o.kubectl, args...); err != nil {
		return fmt.Errorf("StorageCluster CRD not found, is the odf operator installed?: %w", err)
	}

	o.logger.Info("deploying StorageCluster")
	return support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, storageClusterYAML)
}
