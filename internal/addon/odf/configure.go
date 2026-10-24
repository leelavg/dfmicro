package odf

import (
	"context"
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
	clusterCIDR   string
}

const odfPoll = 2 * time.Second
const odfTimeout = 10 * time.Minute

func (o *odf) configure(ctx context.Context, cfg configureConfig) error {
	if err := o.labelStorageNodes(ctx); err != nil {
		return err
	}

	switch {
	case cfg.externalCeph:
		return o.configureRookProvider(ctx, cfg.includeCephFS)
	case cfg.connectTo != "" && !cfg.clientOnly:
		return o.configureRookClient(ctx, cfg.connectTo, cfg.includeCephFS)
	case cfg.clientOnly:
		return o.configureClient(ctx, cfg)
	default:
		return o.configureStorage(ctx, cfg)
	}
}

func (o *odf) configureClient(ctx context.Context, cfg configureConfig) error {
	o.logger.Info("patching external-snapshotter-operator CSV")
	if err := o.patchSnapshotCSV(ctx); err != nil {
		return err
	}

	o.logger.Info("patching ocs-client-operator CSV console deployment")
	if err := o.patchClientConsoleCSV(ctx); err != nil {
		return err
	}

	if err := o.applyDrivers(ctx, cfg.includeCephFS); err != nil {
		return err
	}
	if cfg.connectTo != "" {
		return o.configureOdfClient(ctx, cfg.connectTo, cfg.clusterCIDR)
	}
	return nil
}

func (o *odf) configureStorage(ctx context.Context, cfg configureConfig) error {
	crd := "storageclusters.ocs.openshift.io"
	o.logger.Info("waiting for CRD", "name", crd)
	if err := waitForCRD(ctx, o.runner, o.kubectl, o.kubeconfig, crd); err != nil {
		return err
	}
	o.logger.Info("CRD established", "name", crd)
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
	crd := "drivers.csi.ceph.io"
	o.logger.Info("waiting for CRD", "name", crd)
	if err := waitForCRD(ctx, o.runner, o.kubectl, o.kubeconfig, crd); err != nil {
		return err
	}
	o.logger.Info("CRD established", "name", crd)
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

func (o *odf) patchOCSSubscription(ctx context.Context) error {
	o.logger.Info("waiting for subscription", "name", "ocs-operator")
	name, err := waitForSub(ctx, o.runner, o.kubectl, o.kubeconfig,
		`jsonpath={.items[?(@.spec.name=="ocs-operator")].metadata.name}`,
		func(name string) bool { return name != "" },
	)
	if err != nil {
		return err
	}
	o.logger.Info("subscription created", "name", name)
	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "subscription", name, "-n", "openshift-storage",
		"--type=merge", "-p", `{"spec":{"config":{"env":[{"name":"SINGLE_NODE","value":"true"}]}}}`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}

func (o *odf) applyPackageManifest(ctx context.Context) error {
	o.logger.Info("waiting for ocs-operator subscription readiness")
	output, err := waitForSub(ctx, o.runner, o.kubectl, o.kubeconfig,
		`jsonpath={.items[?(@.spec.name=="ocs-operator")].metadata.name},{.items[?(@.spec.name=="ocs-operator")].spec.channel},{.items[?(@.spec.name=="ocs-operator")].spec.name},{.items[?(@.spec.name=="ocs-operator")].status.installedCSV}`,
		func(output string) bool {
			parts := strings.SplitN(output, ",", 4)
			return len(parts) == 4 && parts[3] != ""
		},
	)
	if err != nil {
		return err
	}
	parts := strings.SplitN(output, ",", 4)
	name, channel, pkg, csv := parts[0], parts[1], parts[2], parts[3]
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
	name := "external-snapshotter-operator"
	o.logger.Info("waiting for CSV", "name", name)
	csvName, err := waitForCSV(ctx, o.runner, o.kubectl, o.kubeconfig, name,
		`jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/odf-external-snapshotter-operator\.openshift-storage)].metadata.name}`)
	if err != nil {
		return err
	}
	o.logger.Info("CSV found", "name", name, "csv", csvName)

	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "csv", csvName, "-n", "openshift-storage",
		"--type=json", "-p", `[{"op":"replace","path":"/spec/install/spec/deployments/0/spec/replicas","value":1}]`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}

func (o *odf) patchClientConsoleCSV(ctx context.Context) error {
	name := "ocs-client-operator"
	o.logger.Info("waiting for CSV", "name", name)
	csvName, err := waitForCSV(ctx, o.runner, o.kubectl, o.kubeconfig, name,
		`jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/ocs-client-operator\.openshift-storage)].metadata.name}`)
	if err != nil {
		return err
	}
	o.logger.Info("CSV found", "name", name, "csv", csvName)

	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "csv", csvName, "-n", "openshift-storage",
		"--type=json", "-p", `[{"op":"replace","path":"/spec/install/spec/deployments/1/spec/replicas","value":0}]`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}

func (o *odf) patchODFConsoleCSV(ctx context.Context) error {
	name := "odf-operator"
	o.logger.Info("waiting for CSV", "name", name)
	csvName, err := waitForCSV(ctx, o.runner, o.kubectl, o.kubeconfig, name,
		`jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/odf-operator\.openshift-storage)].metadata.name}`)
	if err != nil {
		return err
	}
	o.logger.Info("CSV found", "name", name, "csv", csvName)

	_, err = o.runner.Run(ctx, o.kubectl,
		"patch", "csv", csvName, "-n", "openshift-storage",
		"--type=json", "-p", `[{"op":"replace","path":"/spec/install/spec/deployments/1/spec/replicas","value":0}]`,
		"--kubeconfig", o.kubeconfig,
	)
	return err
}
