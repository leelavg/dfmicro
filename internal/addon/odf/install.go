package odf

import (
	"context"
	"embed"

	"dfmicro/internal/support"
)

//go:embed shims/crd/*.yaml shims/cr/*.yaml
var shimsFS embed.FS

type installConfig struct {
	CatalogImage string
	Channel      string
	SubNames     []string
	Version      string
}

func (o *odf) Install(ctx context.Context, cfg installConfig) error {
	o.logger.Info("applying shim CRDs")
	if err := support.ApplyDir(ctx, o.runner, o.kubectl, o.kubeconfig, shimsFS, "shims/crd"); err != nil {
		return err
	}

	o.logger.Info("applying ClusterVersion")
	cv, err := support.Render(clusterVersionTmpl, map[string]string{"Channel": cfg.Channel, "Version": cfg.Version})
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, cv); err != nil {
		return err
	}

	o.logger.Info("applying namespace")
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, namespaceTmpl); err != nil {
		return err
	}

	o.logger.Info("applying catalog source")
	catsrc, err := support.Render(catalogTmpl, map[string]string{"CatalogImage": cfg.CatalogImage})
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, catsrc); err != nil {
		return err
	}

	o.logger.Info("applying operator group")
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, operatorGroupTmpl); err != nil {
		return err
	}

	o.logger.Info("applying shim CRs")
	if err := support.ApplyDir(ctx, o.runner, o.kubectl, o.kubeconfig, shimsFS, "shims/cr"); err != nil {
		return err
	}

	for _, sub := range cfg.SubNames {
		o.logger.Info("applying subscription", "name", sub)
		singleNode := ""
		if sub == "ocs-operator" {
			singleNode = "true"
		}
		s, err := support.Render(subscriptionTmpl, map[string]string{"SubName": sub, "Channel": cfg.Channel, "SingleNode": singleNode})
		if err != nil {
			return err
		}
		if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, s); err != nil {
			return err
		}
	}

	o.logger.Info("labeling nodes")
	args := []string{"label", "nodes", "--all", "cluster.ocs.openshift.io/openshift-storage=", "--overwrite"}
	if o.kubeconfig != "" {
		args = append(args, "--kubeconfig", o.kubeconfig)
	}
	_, err = o.runner.Run(ctx, o.kubectl, args...)
	return err
}
