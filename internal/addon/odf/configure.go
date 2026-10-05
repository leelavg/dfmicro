package odf

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dfmicro/internal/support"
)

type configureConfig struct {
	clientOnly    bool
	includeCephFS bool
	multiNode     bool
	hostNetwork   bool
	externalCeph  bool
	connectTo     string
}

const odfPollInterval = 2 * time.Second

func (o *odf) poll(ctx context.Context, description string, check func() (bool, error)) error {
	for range int(10 * time.Minute / odfPollInterval) {
		if err := ctx.Err(); err != nil {
			return err
		}
		ready, err := check()
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		time.Sleep(odfPollInterval)
	}
	return fmt.Errorf("timed out waiting for %s", description)
}

func (o *odf) configure(ctx context.Context, cfg configureConfig) error {
	if err := o.labelStorageNodes(ctx); err != nil {
		return err
	}

	switch {
	case cfg.externalCeph:
		if err := o.waitForCRD(ctx, "cephclusters.ceph.rook.io"); err != nil {
			return err
		}
		return o.configureRookProvider(ctx, cfg.includeCephFS)
	case cfg.connectTo != "" && !cfg.clientOnly:
		if err := o.waitForCRD(ctx, "storageclusters.ocs.openshift.io"); err != nil {
			return err
		}
		return o.configureRookConsumer(ctx, cfg.connectTo, cfg.includeCephFS)
	case cfg.clientOnly:
		if err := o.waitForCRD(ctx, "drivers.csi.ceph.io"); err != nil {
			return err
		}
		if cfg.connectTo != "" {
			if err := o.waitForCRD(ctx, "storageclients.ocs.openshift.io"); err != nil {
				return err
			}
		}
		return o.configureClient(ctx, cfg.connectTo, cfg.includeCephFS)
	default:
		if err := o.waitForCRD(ctx, "storageclusters.ocs.openshift.io"); err != nil {
			return err
		}
		return o.configureStorage(ctx, cfg)
	}
}

func (o *odf) waitForCRD(ctx context.Context, name string) error {
	o.logger.Info("waiting for CRD creation", "name", name)
	if _, err := o.runner.Run(ctx, o.kubectl,
		"wait", "--for=create", "--timeout=10m", "crd/"+name,
		"--kubeconfig", o.kubeconfig,
	); err != nil {
		return fmt.Errorf("wait for CRD %s creation: %w", name, err)
	}
	o.logger.Info("CRD created", "name", name)
	o.logger.Info("waiting for CRD establishment", "name", name)
	if _, err := o.runner.Run(ctx, o.kubectl,
		"wait", "--for=condition=Established", "--timeout=10m", "crd/"+name,
		"--kubeconfig", o.kubeconfig,
	); err != nil {
		return fmt.Errorf("wait for CRD %s establishment: %w", name, err)
	}
	o.logger.Info("CRD established", "name", name)
	return nil
}

func (o *odf) configureClient(ctx context.Context, connectTo string, includeCephFS bool) error {
	o.logger.Info("patching external-snapshotter-operator CSV")
	if err := o.patchSnapshotCSV(ctx); err != nil {
		return err
	}

	o.logger.Info("patching ocs-client-operator CSV console deployment")
	if err := o.patchClientConsoleCSV(ctx); err != nil {
		return err
	}

	if err := o.applyDrivers(ctx, includeCephFS); err != nil {
		return err
	}
	if connectTo != "" {
		return o.configureOdfClient(ctx, connectTo)
	}
	return nil
}

func (o *odf) configureStorage(ctx context.Context, cfg configureConfig) error {
	if !cfg.multiNode {
		o.logger.Info("patching ocs-operator subscription with SINGLE_NODE")
		if err := o.patchOCSSubscription(ctx); err != nil {
			return err
		}
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

	if err := o.applyDrivers(ctx, cfg.includeCephFS); err != nil {
		return err
	}

	o.logger.Info("applying StorageCluster")
	scVars := map[string]string{
		"IncludeCephFS": strconv.FormatBool(cfg.includeCephFS),
		"HostNetwork":   strconv.FormatBool(cfg.hostNetwork),
	}
	sc, err := support.Render(storageclusterTmpl, scVars)
	if err != nil {
		return err
	}
	return support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, sc)
}

func (o *odf) labelStorageNodes(ctx context.Context) error {
	o.logger.Info("labeling nodes")
	_, err := o.runner.Run(ctx, o.kubectl, "label", "nodes", "--all",
		"cluster.ocs.openshift.io/openshift-storage=", "--overwrite", "--kubeconfig", o.kubeconfig)
	return err
}

func (o *odf) applyDrivers(ctx context.Context, includeCephFS bool) error {
	if includeCephFS {
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

func (o *odf) ocsSubscriptionName(ctx context.Context) (string, error) {
	o.logger.Info("waiting for ODF subscription", "name", "ocs-operator")
	var name string
	err := o.poll(ctx, "subscription with spec.name=ocs-operator", func() (bool, error) {
		result, err := o.runner.Run(ctx, o.kubectl,
			"get", "subscription", "-n", "openshift-storage",
			"-o", `jsonpath={.items[?(@.spec.name=="ocs-operator")].metadata.name}`,
			"--kubeconfig", o.kubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("failed to list subscriptions: %w", err)
		}
		name = strings.TrimSpace(result.Stdout)
		if name != "" {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return "", err
	}
	o.logger.Info("ODF subscription created", "name", name)
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
	o.logger.Info("waiting for ocs-operator subscription readiness", "name", name)
	var channel, pkg, csv string
	err = o.poll(ctx, "ocs-operator subscription readiness", func() (bool, error) {
		result, err := o.runner.Run(ctx, o.kubectl,
			"get", "subscription", name, "-n", "openshift-storage",
			"-o", "jsonpath={.spec.channel},{.spec.name},{.status.installedCSV}",
			"--kubeconfig", o.kubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("failed to get ocs-operator subscription: %w", err)
		}
		parts := strings.SplitN(strings.TrimSpace(result.Stdout), ",", 3)
		if len(parts) != 3 || parts[2] == "" {
			return false, nil
		}
		channel, pkg, csv = parts[0], parts[1], parts[2]
		return true, nil
	})
	if err != nil {
		return err
	}
	o.logger.Info("ocs-operator subscription ready", "name", name, "csv", csv)

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
	csvName, err := o.csvName(ctx, "external-snapshotter-operator",
		`jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/odf-external-snapshotter-operator\.openshift-storage)].metadata.name}`)
	if err != nil {
		return err
	}

	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "csv", csvName, "-n", "openshift-storage",
		"--type=json", "-p", `[{"op":"replace","path":"/spec/install/spec/deployments/0/spec/replicas","value":1}]`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}

func (o *odf) patchClientConsoleCSV(ctx context.Context) error {
	csvName, err := o.csvName(ctx, "ocs-client-operator",
		`jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/ocs-client-operator\.openshift-storage)].metadata.name}`)
	if err != nil {
		return err
	}

	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "csv", csvName, "-n", "openshift-storage",
		"--type=json", "-p", `[{"op":"replace","path":"/spec/install/spec/deployments/1/spec/replicas","value":0}]`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}

func (o *odf) patchODFConsoleCSV(ctx context.Context) error {
	csvName, err := o.csvName(ctx, "odf-operator",
		`jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/odf-operator\.openshift-storage)].metadata.name}`)
	if err != nil {
		return err
	}

	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "csv", csvName, "-n", "openshift-storage",
		"--type=json", "-p", `[{"op":"replace","path":"/spec/install/spec/deployments/1/spec/replicas","value":0}]`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}

func (o *odf) csvName(ctx context.Context, name, output string) (string, error) {
	o.logger.Info("waiting for CSV", "name", name)
	var csvName string
	err := o.poll(ctx, name+" CSV", func() (bool, error) {
		result, err := o.runner.Run(ctx, o.kubectl,
			"get", "csv", "-n", "openshift-storage",
			"-o", output,
			"--kubeconfig", o.kubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("failed to find %s CSV: %w", name, err)
		}
		csvName = strings.TrimSpace(result.Stdout)
		return csvName != "", nil
	})
	if err != nil {
		return "", err
	}
	o.logger.Info("CSV found", "name", name, "csv", csvName)
	return csvName, nil
}
