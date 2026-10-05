package odf

import (
	"context"
	"embed"
	"slices"

	"dfmicro/internal/support"
)

//go:embed shims/crd/*.yaml shims/cr/*.yaml shims/rbac/*.yaml shims/oauth/*.yaml resources/*.yaml
var odfFS embed.FS

type installConfig struct {
	catalogImage string
	channel      string
	subNames     []string
	version      string
	shims        bool
	baseDomain   string
	clusterCIDR  string
	serviceCIDR  string
}

func (o *odf) install(ctx context.Context, cfg installConfig) error {
	o.logger.Info("applying shim CRDs")
	if err := support.ApplyDir(ctx, o.runner, o.kubectl, o.kubeconfig, odfFS, "shims/crd"); err != nil {
		return err
	}
	if cfg.shims {
		return nil
	}

	o.logger.Info("applying ClusterVersion")
	cv, err := support.Render(clusterVersionTmpl, map[string]string{
		"Channel":   cfg.channel,
		"Version":   cfg.version,
		"ClusterID": clusterID(o.clusterName),
	})
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
	catsrc, err := support.Render(catalogTmpl, map[string]string{"CatalogImage": cfg.catalogImage})
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
	if err := o.applyClusterResources(ctx, map[string]string{
		"BaseDomain":  cfg.baseDomain,
		"ClusterCIDR": cfg.clusterCIDR,
		"ServiceCIDR": cfg.serviceCIDR,
	}); err != nil {
		return err
	}

	o.logger.Info("applying OCP implicit RBAC")
	if err := support.ApplyDir(ctx, o.runner, o.kubectl, o.kubeconfig, odfFS, "shims/rbac"); err != nil {
		return err
	}

	o.logger.Info("applying OAuth shims")
	if err := support.ApplyDir(ctx, o.runner, o.kubectl, o.kubeconfig, odfFS, "shims/oauth"); err != nil {
		return err
	}

	if slices.Contains(cfg.subNames, "rook-ceph-operator") {
		o.logger.Info("applying OCS operator config")
		if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, ocsOperatorConfigTmpl); err != nil {
			return err
		}
	}

	for _, sub := range cfg.subNames {
		o.logger.Info("applying subscription", "name", sub)
		s, err := support.Render(subscriptionTmpl, map[string]string{"SubName": sub, "Channel": cfg.channel})
		if err != nil {
			return err
		}
		if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, s); err != nil {
			return err
		}
	}
	return nil
}

func (o *odf) applyClusterResources(ctx context.Context, vars map[string]string) error {
	for _, name := range []string{"00-dns.yaml", "01-infrastructure.yaml", "02-network.yaml"} {
		data, err := odfFS.ReadFile("shims/cr/" + name)
		if err != nil {
			return err
		}
		resource, err := support.Render(string(data), vars)
		if err != nil {
			return err
		}
		if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, resource); err != nil {
			return err
		}
	}
	return nil
}
