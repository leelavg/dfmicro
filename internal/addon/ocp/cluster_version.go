package ocp

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

//go:embed assets/cluster-version-crd.yaml
var clusterVersionCRD string

type ClusterVersion struct {
	runner execx.Runner
}

func NewClusterVersion(runner execx.Runner) *ClusterVersion {
	return &ClusterVersion{runner: runner}
}

func (c *ClusterVersion) ApplyCRD(ctx context.Context, kubeconfig string) error {
	return support.ApplyYAML(ctx, c.runner, support.KubeClient, kubeconfig, clusterVersionCRD)
}

func (c *ClusterVersion) Apply(ctx context.Context, kubeconfig, channel string) error {
	result, err := execx.Run(ctx, c.runner, support.KubeClient,
		"get", "namespace", "kube-system", "-o=jsonpath={.metadata.uid}",
		"--kubeconfig="+kubeconfig,
	)
	if err != nil {
		return fmt.Errorf("get kube-system namespace UID: %w", err)
	}
	clusterID := strings.TrimSpace(result.Stdout)
	if clusterID == "" {
		return fmt.Errorf("kube-system namespace UID is empty")
	}

	clusterVersion, err := support.Render(clusterVersionTmpl, map[string]string{
		"Channel":   channel,
		"ClusterID": clusterID,
	})
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, c.runner, support.KubeClient, kubeconfig, clusterVersion); err != nil {
		return fmt.Errorf("apply ClusterVersion: %w", err)
	}
	return nil
}

func (c *ClusterVersion) PatchStatus(ctx context.Context, kubeconfig, version string) error {
	versionJSON, err := json.Marshal(version)
	if err != nil {
		return err
	}
	statusPatch, err := support.Render(clusterVersionStatusPatchTmpl, map[string]string{"Version": string(versionJSON)})
	if err != nil {
		return err
	}
	_, err = execx.Run(ctx, c.runner, support.KubeClient, "patch", "clusterversion/version", "--type=merge", "--patch="+statusPatch, "--kubeconfig="+kubeconfig)
	if err != nil {
		return fmt.Errorf("patch ClusterVersion status: %w", err)
	}
	return nil
}

const clusterVersionTmpl = `apiVersion: config.openshift.io/v1
kind: ClusterVersion
metadata:
  name: version
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  channel: {{.Channel}}
  clusterID: {{.ClusterID}}
`

const clusterVersionStatusPatchTmpl = `{
  "status": {
    "desired": {
      "version": {{.Version}}
    },
    "history": [
      {
        "state": "Completed",
        "version": {{.Version}},
        "completionTime": "2026-01-01T00:00:00Z"
      }
    ],
    "version": {{.Version}}
  }
}
`
