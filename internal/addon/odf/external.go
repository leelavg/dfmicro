package odf

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

const providerNamespace = "openshift-storage"

type providerNames struct {
	cephCluster    string
	blockPool      string
	filesystem     string
	filesystemMeta string
	filesystemData string
}

func newProviderNames(cluster string) providerNames {
	prefix := "rook-prov"
	if cluster != "" {
		prefix = cluster
	}
	return providerNames{
		cephCluster:    prefix + "-cephcluster",
		blockPool:      prefix + "-rbd",
		filesystem:     prefix + "-cephfs",
		filesystemMeta: prefix + "-cephfs-metadata",
		filesystemData: prefix + "-cephfs-data0",
	}
}

var reImage = regexp.MustCompile(`"image"\s*:\s*"([^"\r\n]+)`)

type externalResource struct {
	Name string            `json:"name"`
	Kind string            `json:"kind"`
	Data map[string]string `json:"data"`
}

func (o *odf) configureRookProvider(ctx context.Context, includeCephFS bool) error {
	o.logger.Info("reading Rook Ceph image")
	image, err := o.rookCephImage(ctx)
	if err != nil {
		return err
	}
	toolboxImage, err := o.rookOperatorImage(ctx)
	if err != nil {
		return err
	}
	names := newProviderNames(o.cluster)
	vars := map[string]string{
		"CephImage":      image,
		"ToolboxImage":   toolboxImage,
		"CephCluster":    names.cephCluster,
		"BlockPool":      names.blockPool,
		"Filesystem":     names.filesystem,
		"FilesystemMeta": names.filesystemMeta,
		"FilesystemData": names.filesystemData,
	}
	resources := []struct {
		name string
		tmpl string
	}{
		{"Rook Ceph SCC", rookProviderSccTmpl},
		{"Rook provider CephCluster", rookProviderCephClusterTmpl},
		{"Rook toolbox", rookProviderToolboxTmpl},
		{"Rook provider block pool", rookProviderBlockPoolTmpl},
	}
	for _, resource := range resources {
		o.logger.Info("applying resource", "name", resource.name)
		rendered, err := support.Render(resource.tmpl, vars)
		if err != nil {
			return err
		}
		if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, rendered); err != nil {
			return err
		}
	}
	if includeCephFS {
		o.logger.Info("applying resource", "name", "Rook provider filesystem")
		resource, err := support.Render(rookProviderFilesystemTmpl, vars)
		if err != nil {
			return err
		}
		return support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, resource)
	}
	return nil
}

func (o *odf) rookOperatorImage(ctx context.Context) (string, error) {
	result, err := o.runner.Run(ctx, o.kubectl, "get", "csv", "-n", providerNamespace,
		"-o", "jsonpath={.items[?(@.metadata.labels.operators\\.coreos\\.com/rook-ceph-operator\\.openshift-storage)].spec.install.spec.deployments[0].spec.template.spec.containers[0].image}", "--kubeconfig", o.kubeconfig)
	if err != nil {
		return "", fmt.Errorf("read Rook operator image: %w", err)
	}
	image := strings.TrimSpace(result.Stdout)
	if image == "" {
		return "", fmt.Errorf("Rook operator image is empty")
	}
	return image, nil
}

func (o *odf) configureRookConsumer(ctx context.Context, sourceName string, includeCephFS bool) error {
	if err := o.patchODFConsoleCSV(ctx); err != nil {
		return err
	}
	if err := o.patchSnapshotCSV(ctx); err != nil {
		return err
	}
	if err := o.applyDrivers(ctx, includeCephFS); err != nil {
		return err
	}

	sourceKubeconfig, err := rootconfig.Kubeconfig(sourceName)
	if err != nil {
		return fmt.Errorf("could not load provider kubeconfig for %q: %w", sourceName, err)
	}
	o.logger.Info("exporting external Ceph details", "source", sourceName)
	details, err := o.exportExternalDetails(ctx, sourceKubeconfig, sourceName, includeCephFS)
	if err != nil {
		return fmt.Errorf("export external Ceph details from %q: %w", sourceName, err)
	}

	secret, err := support.Render(externalDetailsSecretTmpl, map[string]string{"Details": details})
	if err != nil {
		return err
	}
	o.logger.Info("applying external Ceph details")
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, secret); err != nil {
		return err
	}

	o.logger.Info("applying external StorageCluster")
	return support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, externalStorageClusterTmpl)
}

func (o *odf) configureOdfClient(ctx context.Context, providerName string) error {
	providerKubeconfig, err := rootconfig.Kubeconfig(providerName)
	if err != nil {
		return fmt.Errorf("could not load provider kubeconfig for %q: %w", providerName, err)
	}
	ready, err := o.odfClientReady(ctx, providerKubeconfig, providerName)
	if err != nil {
		return err
	}
	if ready {
		o.logger.Info("ODF client already connected", "provider", providerName, "client", o.cluster)
		return nil
	}

	consumer, err := support.Render(storageConsumerTmpl, map[string]string{"ClientCluster": o.cluster})
	if err != nil {
		return err
	}
	o.logger.Info("applying StorageConsumer", "cluster", providerName, "name", o.cluster)
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, providerKubeconfig, consumer); err != nil {
		return err
	}

	ticket, endpoint, err := o.odfOnboardingData(ctx, providerKubeconfig)
	if err != nil {
		return err
	}
	client, err := support.Render(storageClientTmpl, map[string]string{
		"ProviderCluster": providerName,
		"Ticket":          ticket,
		"Endpoint":        endpoint,
	})
	if err != nil {
		return err
	}
	o.logger.Info("applying StorageClient", "name", providerName)
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, client); err != nil {
		return err
	}

	o.logger.Info("waiting for StorageClient", "name", providerName)
	if _, err := o.runner.Run(ctx, o.kubectl,
		"wait", "--for=jsonpath={.status.phase}=Connected", "--timeout=10m",
		"storageclient/"+providerName, "--kubeconfig", o.kubeconfig,
	); err != nil {
		return fmt.Errorf("wait for StorageClient %s: %w", providerName, err)
	}
	o.logger.Info("StorageClient connected", "name", providerName)
	return nil
}

func (o *odf) odfClientReady(ctx context.Context, providerKubeconfig, providerName string) (bool, error) {
	consumer, err := o.runner.Run(ctx, o.kubectl,
		"get", "storageconsumer", o.cluster, "-n", providerNamespace,
		"-o", "jsonpath={.status.state}", "--kubeconfig", providerKubeconfig,
	)
	if err != nil {
		return false, nil
	}
	client, err := o.runner.Run(ctx, o.kubectl,
		"get", "storageclient", providerName,
		"-o", "jsonpath={.status.phase}", "--kubeconfig", o.kubeconfig,
	)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(consumer.Stdout) == "Ready" && strings.TrimSpace(client.Stdout) == "Connected", nil
}

func (o *odf) odfOnboardingData(ctx context.Context, providerKubeconfig string) (string, string, error) {
	var ticket, endpoint string
	err := o.poll(ctx, "StorageConsumer onboarding data", func() (bool, error) {
		result, err := o.runner.Run(ctx, o.kubectl,
			"get", "storageconsumer", o.cluster, "-n", providerNamespace,
			"-o", "jsonpath={.status.onboardingTicketSecret.name}",
			"--kubeconfig", providerKubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("read StorageConsumer status: %w", err)
		}
		secretName := strings.TrimSpace(result.Stdout)
		if secretName == "" {
			return false, nil
		}

		result, err = o.runner.Run(ctx, o.kubectl,
			"get", "secret", secretName, "-n", providerNamespace,
			"-o", "jsonpath={.data.onboarding-token}", "--kubeconfig", providerKubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("read onboarding Secret %s: %w", secretName, err)
		}
		ticketBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(result.Stdout))
		if err != nil {
			return false, fmt.Errorf("decode onboarding Secret %s: %w", secretName, err)
		}

		result, err = o.runner.Run(ctx, o.kubectl,
			"get", "storagecluster", "ocs-storagecluster", "-n", providerNamespace,
			"-o", "jsonpath={.status.storageProviderEndpoint}", "--kubeconfig", providerKubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("read provider endpoint: %w", err)
		}
		endpoint = strings.TrimSpace(result.Stdout)
		if endpoint == "" {
			return false, nil
		}
		ticket = string(ticketBytes)
		return true, nil
	})
	if err != nil {
		return "", "", err
	}
	return ticket, endpoint, nil
}

func (o *odf) rookCephImage(ctx context.Context) (string, error) {
	result, err := o.runner.Run(ctx, o.kubectl, "get", "csv", "-n", providerNamespace,
		"-o", `jsonpath={.items[?(@.metadata.labels.operators\.coreos\.com/rook-ceph-operator\.openshift-storage)].metadata.annotations.alm-examples}`,
		"--kubeconfig", o.kubeconfig)
	if err != nil {
		return "", fmt.Errorf("read Rook CSV examples: %w", err)
	}
	examples := strings.TrimSpace(result.Stdout)
	if examples == "" {
		return "", fmt.Errorf("Rook CSV alm-examples annotation is empty")
	}
	var resources []struct {
		Kind string `json:"kind"`
		Spec struct {
			CephVersion struct {
				Image string `json:"image"`
			} `json:"cephVersion"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(examples), &resources); err == nil {
		for _, resource := range resources {
			if resource.Kind == "CephCluster" && resource.Spec.CephVersion.Image != "" {
				return resource.Spec.CephVersion.Image, nil
			}
		}
	}
	match := reImage.FindStringSubmatch(examples)
	if len(match) == 2 {
		return match[1], nil
	}
	return "", fmt.Errorf("Rook Ceph image not found in CSV alm-examples")
}

func (o *odf) exportExternalDetails(ctx context.Context, kubeconfig, sourceName string, includeCephFS bool) (string, error) {
	result, err := o.runner.Run(ctx, o.kubectl, "get", "configmap", "rook-ceph-external-cluster-script-config", "-n", providerNamespace, "-o", "jsonpath={.data.script}", "--kubeconfig", kubeconfig)
	if err != nil {
		return "", err
	}
	script, err := base64.StdEncoding.DecodeString(strings.TrimSpace(result.Stdout))
	if err != nil {
		return "", fmt.Errorf("decode exporter script: %w", err)
	}

	pod, err := o.runner.Run(ctx, o.kubectl, "get", "pods", "-n", providerNamespace, "-l", "app=rook-ceph-tools", "-o", "jsonpath={.items[0].metadata.name}", "--kubeconfig", kubeconfig)
	if err != nil {
		return "", fmt.Errorf("find Rook toolbox: %w", err)
	}
	toolbox := strings.TrimSpace(pod.Stdout)
	if toolbox == "" {
		return "", fmt.Errorf("Rook toolbox pod not found")
	}

	names := newProviderNames(sourceName)
	args := []string{"exec", "-n", providerNamespace, toolbox, "--kubeconfig", kubeconfig, "--", "python3", "-c", string(script), "--namespace", providerNamespace, "--rbd-data-pool-name", names.blockPool, "--format", "json"}
	if includeCephFS {
		args = append(args, "--cephfs-filesystem-name", names.filesystem, "--cephfs-metadata-pool-name", names.filesystemMeta, "--cephfs-data-pool-name", names.filesystemData)
	}
	result, err = o.runner.Run(ctx, o.kubectl, args...)
	if err != nil {
		return "", fmt.Errorf("run Rook external exporter: %w", err)
	}
	var resources []externalResource
	if err := json.Unmarshal([]byte(result.Stdout), &resources); err != nil {
		return "", fmt.Errorf("parse exporter output: %w", err)
	}
	required := map[string]bool{
		"rook-ceph-mon-endpoints":  false,
		"rook-ceph-mon":            false,
		"rook-csi-rbd-node":        false,
		"rook-csi-rbd-provisioner": false,
		"ceph-rbd":                 false,
	}
	if includeCephFS {
		required["rook-csi-cephfs-node"] = false
		required["rook-csi-cephfs-provisioner"] = false
		required["cephfs"] = false
	}
	for _, resource := range resources {
		if _, ok := required[resource.Name]; ok {
			required[resource.Name] = true
		}
	}
	for name, found := range required {
		if !found {
			return "", fmt.Errorf("Rook external exporter did not return %s", name)
		}
	}
	return strings.TrimSpace(result.Stdout), nil
}
