package odf

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

const (
	providerNamespace      = "openshift-storage"
	providerCephCluster    = "rook-prov-cephcluster"
	providerBlockPool      = "rook-prov-rbd"
	providerFilesystem     = "rook-prov-cephfs"
	providerFilesystemMeta = "rook-prov-cephfs-metadata"
	providerFilesystemData = "rook-prov-cephfs-data0"
)

var reImage = regexp.MustCompile(`"image"\s*:\s*"([^"\r\n]+)`)

type externalResource struct {
	Name string            `json:"name"`
	Kind string            `json:"kind"`
	Data map[string]string `json:"data"`
}

func (o *odf) configureRookProvider(ctx context.Context, includeCephFS bool) error {
	image, err := o.rookCephImage(ctx)
	if err != nil {
		return err
	}
	vars := map[string]string{"CephImage": image}
	for _, tmpl := range []string{rookProviderCephClusterTmpl, rookProviderBlockPoolTmpl} {
		resource, err := support.Render(tmpl, vars)
		if err != nil {
			return err
		}
		if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, resource); err != nil {
			return err
		}
	}
	if includeCephFS {
		resource, err := support.Render(rookProviderFilesystemTmpl, vars)
		if err != nil {
			return err
		}
		return support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, resource)
	}
	return nil
}

func (o *odf) configureRookConsumer(ctx context.Context, sourceName string, includeCephFS bool) error {
	if _, err := o.runner.Run(ctx, o.kubectl, "get", "crd", "storageclusters.ocs.openshift.io", "--kubeconfig", o.kubeconfig); err != nil {
		return fmt.Errorf("StorageCluster CRD not found, is the odf operator installed?: %w", err)
	}
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
	details, err := o.exportExternalDetails(ctx, sourceKubeconfig, includeCephFS)
	if err != nil {
		return fmt.Errorf("export external Ceph details from %q: %w", sourceName, err)
	}

	secret, err := support.Render(externalDetailsSecretTmpl, map[string]string{"Details": details})
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, secret); err != nil {
		return err
	}

	return support.ApplyYAML(ctx, o.runner, o.kubectl, o.kubeconfig, externalStorageClusterTmpl)
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

func (o *odf) exportExternalDetails(ctx context.Context, kubeconfig string, includeCephFS bool) (string, error) {
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

	file, err := os.CreateTemp("", "dfmicro-ceph-export-*.py")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(script); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	remotePath := "/tmp/dfmicro-ceph-export.py"
	if _, err := o.runner.Run(ctx, o.kubectl, "cp", path, providerNamespace+"/"+toolbox+":"+remotePath, "--kubeconfig", kubeconfig); err != nil {
		return "", fmt.Errorf("copy exporter to Rook toolbox: %w", err)
	}
	defer o.runner.Run(ctx, o.kubectl, "exec", "-n", providerNamespace, toolbox, "--kubeconfig", kubeconfig, "--", "rm", "-f", remotePath)

	args := []string{"exec", "-n", providerNamespace, toolbox, "--kubeconfig", kubeconfig, "--", "python3", remotePath, "--namespace", providerNamespace, "--rbd-data-pool-name", providerBlockPool, "--skip-monitoring-endpoint", "--format", "json"}
	if includeCephFS {
		args = append(args, "--cephfs-filesystem-name", providerFilesystem, "--cephfs-metadata-pool-name", providerFilesystemMeta, "--cephfs-data-pool-name", providerFilesystemData)
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
