package odf

import (
	"context"
	"fmt"
	"strings"

	"dfmicro/internal/support"
)

func (o *odf) Configure(ctx context.Context) error {
	o.logger.Info("checking StorageCluster CRD presence")
	if _, err := o.runner.Run(ctx, o.kubectl, "get", "crd", "storageclusters.ocs.openshift.io", "--kubeconfig", o.kubeconfig); err != nil {
		return fmt.Errorf("StorageCluster CRD not found, is the odf operator installed?: %w", err)
	}

	o.logger.Info("patching ocs-operator subscription with SINGLE_NODE")
	if err := o.patchOCSSubscription(ctx); err != nil {
		return err
	}

	o.logger.Info("labeling nodes")
	if _, err := o.runner.Run(ctx, o.kubectl, "label", "nodes", "--all",
		"cluster.ocs.openshift.io/openshift-storage=", "--overwrite", "--kubeconfig", o.kubeconfig); err != nil {
		return err
	}

	o.logger.Info("applying PackageManifest for ocs-operator")
	if err := o.applyPackageManifest(ctx); err != nil {
		return err
	}

	o.logger.Info("applying resources")
	return support.ApplyDir(ctx, o.runner, o.kubectl, o.kubeconfig, odfFS, "resources")
}

func (o *odf) ocsSubscriptionName(ctx context.Context) (string, error) {
	result, err := o.runner.Run(ctx, o.kubectl,
		"get", "subscription", "-n", "openshift-storage",
		"-o", `jsonpath={.items[?(@.spec.name=="ocs-operator")].metadata.name}`,
		"--kubeconfig", o.kubeconfig,
	)
	if err != nil {
		return "", fmt.Errorf("failed to list subscriptions: %w", err)
	}
	name := strings.TrimSpace(result.Stdout)
	if name == "" {
		return "", fmt.Errorf("no subscription found with spec.name=ocs-operator")
	}
	return name, nil
}

func (o *odf) patchOCSSubscription(ctx context.Context) error {
	name, err := o.ocsSubscriptionName(ctx)
	if err != nil {
		return err
	}
	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "subscription", name, "-n", "openshift-storage",
		"--type=merge", "-p", `{"spec":{"config":{"env":[{"name":"SINGLE_NODE","value":"true"}]}}}`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}

func (o *odf) applyPackageManifest(ctx context.Context) error {
	name, err := o.ocsSubscriptionName(ctx)
	if err != nil {
		return err
	}
	result, err := o.runner.Run(ctx, o.kubectl,
		"get", "subscription", name, "-n", "openshift-storage",
		"-o", "jsonpath={.spec.channel},{.spec.name},{.status.installedCSV}",
		"--kubeconfig", o.kubeconfig,
	)
	if err != nil {
		return fmt.Errorf("failed to get ocs-operator subscription: %w", err)
	}
	parts := strings.SplitN(strings.TrimSpace(result.Stdout), ",", 3)
	if len(parts) != 3 || parts[2] == "" {
		return fmt.Errorf("ocs-operator subscription not ready, installedCSV is empty")
	}
	channel, pkg, csv := parts[0], parts[1], parts[2]

	pm, err := support.Render(packageManifestTmpl, map[string]string{
		"Package": pkg,
		"Channel": channel,
		"CSV":     csv,
	})
	if err != nil {
		return err
	}
	return support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, pm)
}
