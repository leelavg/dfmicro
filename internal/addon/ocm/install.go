package ocm

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

type ocm struct {
	logger *slog.Logger
	runner execx.Runner
}

func (o *ocm) install(ctx context.Context, clusterName string) error {
	kubeconfig, err := rootconfig.Kubeconfig(clusterName)
	if err != nil {
		return fmt.Errorf("could not load kubeconfig for cluster %q: %w", clusterName, err)
	}
	tempDir, err := os.MkdirTemp("", "dfmicro-ocm-")
	if err != nil {
		return fmt.Errorf("create temporary OCM config directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	selectedKubeconfig := filepath.Join(tempDir, "kubeconfig")
	contextName, err := privateContext(ctx, o.runner, kubeconfig, selectedKubeconfig)
	if err != nil {
		return err
	}
	result, err := execx.Run(ctx, o.runner, support.KubeClient, "get", "deployment/cluster-manager",
		"--namespace=open-cluster-management", "--ignore-not-found", "--output=name", "--kubeconfig="+kubeconfig)
	if err != nil {
		return fmt.Errorf("check OCM cluster-manager: %w", err)
	}
	if strings.TrimSpace(result.Stdout) != "" {
		o.logger.Info("OCM cluster-manager already exists", "cluster", clusterName)
		return nil
	}
	clusteradm, err := support.EnsureClusteradm(ctx, o.runner, o.logger)
	if err != nil {
		return err
	}

	o.logger.Info("initializing OCM hub", "cluster", clusterName, "context", contextName)
	args := []string{
		"init",
		"--feature-gates=ManagedClusterAutoApproval=true",
		"--singleton-name=" + clusterName,
		"--create-namespace=true",
		"--wait=true",
		"--kubeconfig=" + selectedKubeconfig,
		"--context=" + contextName,
	}
	// Keep clusteradm's --wait output visible while hub components settle.
	if err := o.runner.RunInteractive(ctx, clusteradm, args...); err != nil {
		return err
	}
	o.logger.Info("scaling OCM cluster-manager", "cluster", clusterName, "replicas", 1)
	_, err = execx.Run(ctx, o.runner, support.KubeClient, "scale", "deployment/cluster-manager", "--replicas=1", "--namespace=open-cluster-management", "--kubeconfig="+kubeconfig)
	if err != nil {
		return fmt.Errorf("scale OCM cluster-manager to one replica: %w", err)
	}
	return nil
}

func privateContext(ctx context.Context, runner execx.Runner, source, destination string) (string, error) {
	result, err := execx.Run(ctx, runner, support.KubeClient, "--kubeconfig", source, "config", "get-contexts", "-o", "name")
	if err != nil {
		return "", fmt.Errorf("list kubeconfig contexts: %w", err)
	}

	var selected string
	for name := range strings.FieldsSeq(result.Stdout) {
		address, err := privateAddress(name)
		if err != nil {
			return "", err
		}
		if address == nil {
			continue
		}

		server, err := execx.Run(ctx, runner, support.KubeClient, "--kubeconfig", source, "config", "view", "--minify", "--context", name, "-o", "jsonpath={.clusters[0].cluster.server}")
		if err != nil {
			return "", fmt.Errorf("inspect API endpoint for context %q: %w", name, err)
		}
		endpoint, err := url.Parse(strings.TrimSpace(server.Stdout))
		if err != nil {
			return "", fmt.Errorf("parse API endpoint for context %q: %w", name, err)
		}
		serverIP := net.ParseIP(endpoint.Hostname())
		if serverIP == nil || !serverIP.Equal(address) {
			continue
		}
		if selected != "" {
			return "", fmt.Errorf("multiple kubeconfig contexts use private API addresses: %q and %q", selected, name)
		}
		selected = name
	}
	if selected == "" {
		return "", fmt.Errorf("no kubeconfig context name matches a private IPv4 API endpoint")
	}
	result, err = execx.Run(ctx, runner, support.KubeClient, "--kubeconfig", source, "config", "view", "--raw", "--flatten", "--minify", "--context", selected, "-o", "json")
	if err != nil {
		return "", fmt.Errorf("extract private kubeconfig context: %w", err)
	}
	if err := os.WriteFile(destination, []byte(result.Stdout), 0o600); err != nil {
		return "", fmt.Errorf("write private kubeconfig: %w", err)
	}
	return selected, nil
}

func privateAddress(contextName string) (net.IP, error) {
	var address net.IP
	for _, candidate := range strings.FieldsFunc(contextName, func(r rune) bool {
		return (r < '0' || r > '9') && r != '.'
	}) {
		if support.ValidateIPv4PrivateCIDR(candidate+"/32") != nil {
			continue
		}
		ip := net.ParseIP(candidate)
		if address != nil && !address.Equal(ip) {
			return nil, fmt.Errorf("context name %q contains multiple private IPv4 addresses", contextName)
		}
		address = ip
	}
	return address, nil
}
