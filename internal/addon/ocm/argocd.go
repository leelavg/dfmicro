package ocm

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

//go:embed assets/argocd
var argocdAssets embed.FS

//go:embed assets/argocd/cluster-secret.yaml.tmpl
var argocdClusterSecretTmpl string

func (o *ocm) installArgoCD(ctx context.Context, kubeconfig string) error {
	tmpDir, err := os.MkdirTemp("", "dfmicro-argocd-")
	if err != nil {
		return fmt.Errorf("create temporary Argo CD directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	assets, err := fs.Sub(argocdAssets, "assets/argocd")
	if err != nil {
		return err
	}
	if err := fs.WalkDir(assets, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(tmpDir, path)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	}); err != nil {
		return fmt.Errorf("write Argo CD manifests: %w", err)
	}
	_, err = execx.Run(ctx, o.runner, support.KubeClient, "apply", "--server-side", "-k", tmpDir, "--kubeconfig", kubeconfig)
	return err
}

func (o *ocm) registerArgoCDCluster(ctx context.Context, hubKubeconfig, clusterName, clusterKubeconfig string) error {
	manifest, err := argocdAssets.ReadFile("assets/argocd/managed-cluster.yaml")
	if err != nil {
		return err
	}
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, clusterKubeconfig, string(manifest)); err != nil {
		return fmt.Errorf("create Argo CD service account: %w", err)
	}

	o.logger.Info("waiting for Argo CD service account token", "cluster", clusterName)
	var encodedToken string
	if err := support.Poll(ctx, time.Second, 2*time.Minute, func(ctx context.Context) (bool, error) {
		result, err := execx.Run(ctx, o.runner, support.KubeClient,
			"--kubeconfig", clusterKubeconfig,
			"get", "secret", "argocd-manager-token", "--namespace=kube-system",
			"--output=jsonpath={.data.token}")
		if err != nil {
			return false, err
		}
		encodedToken = strings.TrimSpace(result.Stdout)
		return encodedToken != "", nil
	}); err != nil {
		return fmt.Errorf("wait for Argo CD service account token: %w", err)
	}
	token, err := base64.StdEncoding.DecodeString(encodedToken)
	if err != nil {
		return fmt.Errorf("decode Argo CD service account token: %w", err)
	}

	result, err := execx.Run(ctx, o.runner, support.KubeClient,
		"--kubeconfig", clusterKubeconfig, "config", "view", "--raw", "--flatten", "--minify", "--output=json")
	if err != nil {
		return fmt.Errorf("read Argo CD cluster endpoint: %w", err)
	}
	var kubeconfigData struct {
		Clusters []struct {
			Cluster struct {
				Server                   string `json:"server"`
				CertificateAuthorityData string `json:"certificate-authority-data"`
			} `json:"cluster"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &kubeconfigData); err != nil {
		return fmt.Errorf("decode Argo CD cluster endpoint: %w", err)
	}
	if len(kubeconfigData.Clusters) == 0 {
		return fmt.Errorf("kubeconfig for cluster %q has no cluster entry", clusterName)
	}
	cluster := kubeconfigData.Clusters[0].Cluster
	if cluster.Server == "" || cluster.CertificateAuthorityData == "" {
		return fmt.Errorf("kubeconfig for cluster %q is missing server or certificate authority data", clusterName)
	}

	config, err := json.Marshal(map[string]any{
		"bearerToken": string(token),
		"tlsClientConfig": map[string]any{
			"insecure": false,
			"caData":   cluster.CertificateAuthorityData,
		},
	})
	if err != nil {
		return err
	}
	secretName, err := json.Marshal(clusterName)
	if err != nil {
		return fmt.Errorf("encode Argo CD cluster name: %w", err)
	}
	server, err := json.Marshal(cluster.Server)
	if err != nil {
		return fmt.Errorf("encode Argo CD cluster endpoint: %w", err)
	}
	configYAML, err := json.Marshal(string(config))
	if err != nil {
		return fmt.Errorf("encode Argo CD cluster configuration: %w", err)
	}
	renderedManifest, err := support.Render(argocdClusterSecretTmpl, map[string]string{
		"SecretName":  clusterName,
		"ClusterName": string(secretName),
		"Server":      string(server),
		"Config":      string(configYAML),
	})
	if err != nil {
		return fmt.Errorf("render Argo CD cluster secret: %w", err)
	}
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, hubKubeconfig, renderedManifest); err != nil {
		return fmt.Errorf("apply Argo CD cluster secret on hub: %w", err)
	}
	return nil
}
