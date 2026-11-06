package odr

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

func (o *odr) applyTrustedCABundle(ctx context.Context, clusterName, namespace, operator, deployment string) error {
	kubeconfig, err := rootconfig.Kubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("load kubeconfig for cluster %q: %w", clusterName, err)
	}

	o.logger.Info("waiting for ODR trusted CA ConfigMap", "cluster", clusterName, "namespace", namespace)
	err = support.Poll(ctx, 2*time.Second, 10*time.Minute, func(ctx context.Context) (bool, error) {
		result, err := o.runner.Run(ctx, support.KubeClient,
			"get", "configmap", "openshift-trusted-cabundle", "-n", namespace,
			"--ignore-not-found", "-o=jsonpath={.metadata.ownerReferences[0].name}",
			"--kubeconfig", kubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("get ODR trusted CA ConfigMap: %w", err)
		}
		return strings.HasPrefix(strings.TrimSpace(result.Stdout), operator+"."), nil
	})
	if err != nil {
		return fmt.Errorf("wait for ODR trusted CA ConfigMap in cluster %q: %w", clusterName, err)
	}

	result, err := support.RunPodmanPrivileged(ctx, o.runner,
		"exec", rootconfig.NodeName(clusterName, 0),
		"cat", "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
	)
	if err != nil {
		return fmt.Errorf("read cluster %q TLS CA bundle: %w", clusterName, err)
	}
	if strings.TrimSpace(result.Stdout) == "" {
		return fmt.Errorf("cluster %q TLS CA bundle is empty", clusterName)
	}
	caBundle, err := json.Marshal(result.Stdout)
	if err != nil {
		return fmt.Errorf("encode cluster %q TLS CA bundle: %w", clusterName, err)
	}
	configMap, err := support.Render(trustedCABundleTmpl, map[string]string{
		"Namespace": namespace,
		"CABundle":  string(caBundle),
	})
	if err != nil {
		return err
	}
	o.logger.Info("applying trusted CA bundle", "cluster", clusterName, "namespace", namespace)
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, kubeconfig, configMap); err != nil {
		return fmt.Errorf("apply trusted CA bundle in cluster %q: %w", clusterName, err)
	}

	result, err = o.runner.Run(ctx, support.KubeClient,
		"get", "deployment", deployment, "-n", namespace,
		"--ignore-not-found", `-o=jsonpath={.metadata.name}{":"}{.status.availableReplicas}`,
		"--kubeconfig", kubeconfig,
	)
	if err != nil {
		return fmt.Errorf("get ODR deployment %s in cluster %q: %w", deployment, clusterName, err)
	}
	deploymentState := strings.TrimSpace(result.Stdout)
	if deploymentState == "" {
		return nil
	}
	_, availableReplicas, ok := strings.Cut(deploymentState, ":")
	if !ok {
		return fmt.Errorf("unexpected ODR deployment status %q", deploymentState)
	}
	available, err := strconv.Atoi(availableReplicas)
	if err == nil && available > 0 {
		return nil
	}

	o.logger.Info("restarting unavailable ODR operator", "cluster", clusterName, "deployment", deployment)
	if _, err := o.runner.Run(ctx, support.KubeClient,
		"rollout", "restart", "deployment/"+deployment, "-n", namespace,
		"--kubeconfig", kubeconfig,
	); err != nil {
		return fmt.Errorf("restart ODR deployment %s in cluster %q: %w", deployment, clusterName, err)
	}
	if _, err := o.runner.Run(ctx, support.KubeClient,
		"rollout", "status", "deployment/"+deployment, "-n", namespace,
		"--timeout=10m", "--kubeconfig", kubeconfig,
	); err != nil {
		return fmt.Errorf("wait for ODR deployment %s in cluster %q: %w", deployment, clusterName, err)
	}
	return nil
}
