package odf

import (
	"context"
	"fmt"
	"strings"

	"dfmicro/internal/support"
)

func (o *odf) configure(ctx context.Context, cfg configureConfig) error {
	if cfg.ClientOnly {
		o.logger.Info("checking Driver CRD")
		if _, err := o.runner.Run(ctx, o.kubectl, "get", "crd", "drivers.csi.ceph.io", "--kubeconfig", o.kubeconfig); err != nil {
			return fmt.Errorf("Driver CRD not found: %w", err)
		}

		o.logger.Info("patching external-snapshotter-operator CSV")
		if err := o.patchSnapshotCSV(ctx); err != nil {
			return err
		}

		o.logger.Info("patching ocs-client-operator CSV console deployment")
		if err := o.patchClientCSV(ctx); err != nil {
			return err
		}

		if cfg.IncludeCephFS {
			o.logger.Info("applying cephfs driver")
			cephfs, err := odfFS.ReadFile("resources/00-cephfs-driver.yaml")
			if err != nil {
				return err
			}
			if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, string(cephfs)); err != nil {
				return err
			}
		}

		o.logger.Info("applying rbd driver")
		rbd, err := odfFS.ReadFile("resources/00-rbd-driver.yaml")
		if err != nil {
			return err
		}
		return support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, string(rbd))
	}

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

	o.logger.Info("patching odf-operator CSV console deployment")
	if err := o.patchODFConsoleCSV(ctx); err != nil {
		return err
	}

	o.logger.Info("patching external-snapshotter-operator CSV")
	if err := o.patchSnapshotCSV(ctx); err != nil {
		return err
	}

	if cfg.IncludeCephFS {
		o.logger.Info("applying cephfs driver")
		cephfs, err := odfFS.ReadFile("resources/00-cephfs-driver.yaml")
		if err != nil {
			return err
		}
		if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, string(cephfs)); err != nil {
			return err
		}
	}

	o.logger.Info("applying rbd driver")
	rbd, err := odfFS.ReadFile("resources/00-rbd-driver.yaml")
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, string(rbd)); err != nil {
		return err
	}

	topolvm, err := odfFS.ReadFile("resources/01-topolvm-immediate-sc.yaml")
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, string(topolvm)); err != nil {
		return err
	}

	o.logger.Info("applying StorageCluster")
	scVars := map[string]string{"IncludeCephFS": ""}
	if cfg.IncludeCephFS {
		scVars["IncludeCephFS"] = "true"
	}
	sc, err := support.Render(storageclusterTmpl, scVars)
	if err != nil {
		return err
	}
	return support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, sc)
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

func (o *odf) patchSnapshotCSV(ctx context.Context) error {
	result, err := o.runner.Run(ctx, o.kubectl,
		"get", "csv", "-n", "openshift-storage",
		"-o", `jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/odf-external-snapshotter-operator\.openshift-storage)].metadata.name}`,
		"--kubeconfig", o.kubeconfig,
	)
	if err != nil {
		return fmt.Errorf("failed to find external-snapshotter-operator CSV: %w", err)
	}
	csvName := strings.TrimSpace(result.Stdout)
	if csvName == "" {
		return fmt.Errorf("external-snapshotter-operator CSV not found")
	}

	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "csv", csvName, "-n", "openshift-storage",
		"--type=json", "-p", `[{"op":"replace","path":"/spec/install/spec/deployments/0/spec/replicas","value":1}]`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}

func (o *odf) patchClientCSV(ctx context.Context) error {
	result, err := o.runner.Run(ctx, o.kubectl,
		"get", "csv", "-n", "openshift-storage",
		"-o", `jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/ocs-client-operator\.openshift-storage)].metadata.name}`,
		"--kubeconfig", o.kubeconfig,
	)
	if err != nil {
		return fmt.Errorf("failed to find ocs-client-operator CSV: %w", err)
	}
	csvName := strings.TrimSpace(result.Stdout)
	if csvName == "" {
		return fmt.Errorf("ocs-client-operator CSV not found")
	}

	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "csv", csvName, "-n", "openshift-storage",
		"--type=json", "-p", `[{"op":"replace","path":"/spec/install/spec/deployments/1/spec/replicas","value":0}]`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}

func (o *odf) patchODFConsoleCSV(ctx context.Context) error {
	result, err := o.runner.Run(ctx, o.kubectl,
		"get", "csv", "-n", "openshift-storage",
		"-o", `jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/odf-operator\.openshift-storage)].metadata.name}`,
		"--kubeconfig", o.kubeconfig,
	)
	if err != nil {
		return fmt.Errorf("failed to find odf-operator CSV: %w", err)
	}
	csvName := strings.TrimSpace(result.Stdout)
	if csvName == "" {
		return fmt.Errorf("odf-operator CSV not found")
	}

	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "csv", csvName, "-n", "openshift-storage",
		"--type=json", "-p", `[{"op":"replace","path":"/spec/install/spec/deployments/1/spec/replicas","value":0}]`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}
