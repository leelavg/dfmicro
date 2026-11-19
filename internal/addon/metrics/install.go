package metrics

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

//go:embed assets/components.yaml
var metricsAssets embed.FS

const (
	metricsManifest    = "assets/components.yaml"
	kubeletCAConfigMap = "kubelet-client-ca"
)

type metrics struct {
	logger      *slog.Logger
	runner      execx.Runner
	clusterName string
	kubeconfig  string
}

func (m *metrics) install(ctx context.Context) error {
	result, err := m.runner.Run(ctx, support.KubeClient, "get", "configmap/"+kubeletCAConfigMap, "-n", "kube-system", "--ignore-not-found", "-o", "name", "--kubeconfig="+m.kubeconfig)
	if err != nil {
		return fmt.Errorf("check kubelet client CA ConfigMap: %w", err)
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return fmt.Errorf("ConfigMap %s is missing from kube-system", kubeletCAConfigMap)
	}

	manifest, err := metricsAssets.ReadFile(metricsManifest)
	if err != nil {
		return fmt.Errorf("read Metrics Server manifest: %w", err)
	}
	m.logger.Info("applying Metrics Server", "cluster", m.clusterName, "version", metricsVersion)
	if err := support.ApplyYAML(ctx, m.runner, support.KubeClient, m.kubeconfig, string(manifest)); err != nil {
		return fmt.Errorf("apply Metrics Server: %w", err)
	}

	m.logger.Info("waiting for Metrics Server", "cluster", m.clusterName)
	if _, err := m.runner.Run(ctx, support.KubeClient, "rollout", "status", "deployment/metrics-server", "-n", "kube-system", "--timeout=3m", "--kubeconfig="+m.kubeconfig); err != nil {
		return fmt.Errorf("wait for Metrics Server deployment: %w", err)
	}
	if _, err := m.runner.Run(ctx, support.KubeClient, "wait", "--for=condition=Available", "apiservice/v1beta1.metrics.k8s.io", "--timeout=3m", "--kubeconfig="+m.kubeconfig); err != nil {
		return fmt.Errorf("wait for Metrics API: %w", err)
	}
	return nil
}

func (m *metrics) uninstall(ctx context.Context) error {
	manifest, err := metricsAssets.ReadFile(metricsManifest)
	if err != nil {
		return fmt.Errorf("read Metrics Server manifest: %w", err)
	}
	f, err := os.CreateTemp("", "dfmicro-metrics-server-*.yaml")
	if err != nil {
		return fmt.Errorf("create temporary Metrics Server manifest: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(manifest); err != nil {
		f.Close()
		return fmt.Errorf("write temporary Metrics Server manifest: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temporary Metrics Server manifest: %w", err)
	}

	m.logger.Info("removing Metrics Server", "cluster", m.clusterName)
	if _, err := m.runner.Run(ctx, support.KubeClient, "delete", "-f", f.Name(), "--ignore-not-found", "--kubeconfig="+m.kubeconfig); err != nil {
		return fmt.Errorf("remove Metrics Server: %w", err)
	}
	return nil
}
