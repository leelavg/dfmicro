package odf

import (
	"context"
	"embed"

	"dfmicro/internal/support"
)

//go:embed shims/crd/*.yaml shims/cr/*.yaml shims/rbac/*.yaml shims/oauth/*.yaml resources/*.yaml
var odfFS embed.FS

type installConfig struct {
	catalogImage string
	channel      string
	subNames     []string
	version      string
}

type configureConfig struct {
	clientOnly    bool
	includeCephFS bool
}

func (o *odf) install(ctx context.Context, cfg installConfig) error {
	o.logger.Info("applying shim CRDs")
	if err := support.ApplyDir(ctx, o.runner, o.kubectl, o.kubeconfig, odfFS, "shims/crd"); err != nil {
		return err
	}

	o.logger.Info("applying ClusterVersion")
	cv, err := support.Render(clusterVersionTmpl, map[string]string{"Channel": cfg.channel, "Version": cfg.version})
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
	if err := support.ApplyDir(ctx, o.runner, o.kubectl, o.kubeconfig, odfFS, "shims/cr"); err != nil {
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
