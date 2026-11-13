package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

const routeDNSStart = "# dfmicro routes begin"
const routeDNSEnd = "# dfmicro routes end"

type routeDNS struct {
	logger *slog.Logger
	runner execx.Runner
}

type routeCluster struct {
	config rootconfig.ClusterConfig
	ip     string
}

func routesCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:   "routes",
		Usage:  "Manage wildcard DNS for cluster routes",
		Action: support.UnknownSubcommand,
		Commands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Create route DNS entries for running clusters",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					routes := &routeDNS{logger: logger, runner: runner}
					return routes.reconcile(ctx)
				},
			},
			{
				Name:  "delete",
				Usage: "Remove dfmicro route DNS entries",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					routes := &routeDNS{logger: logger, runner: runner}
					return routes.delete(ctx)
				},
			},
		},
	}
}

func (r *routeDNS) reconcile(ctx context.Context) error {
	clusters, err := r.runningClusters(ctx)
	if err != nil {
		return err
	}

	var reconcileErrors []error
	for _, cluster := range clusters {
		var blocks []string
		for _, peer := range clusters {
			if peer.config.Name == cluster.config.Name || peer.config.BridgeName != cluster.config.BridgeName {
				continue
			}
			blocks = append(blocks, routeDNSBlock(peer.config.Name, peer.ip))
		}

		if err := r.update(ctx, cluster, blocks); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("cluster %s: %w", cluster.config.Name, err))
		}
	}
	return errors.Join(reconcileErrors...)
}

func (r *routeDNS) delete(ctx context.Context) error {
	clusters, err := r.runningClusters(ctx)
	if err != nil {
		return err
	}

	var deleteErrors []error
	for _, cluster := range clusters {
		if err := r.update(ctx, cluster, nil); err != nil {
			deleteErrors = append(deleteErrors, fmt.Errorf("cluster %s: %w", cluster.config.Name, err))
		}
	}
	return errors.Join(deleteErrors...)
}

func (r *routeDNS) runningClusters(ctx context.Context) ([]routeCluster, error) {
	entries, err := os.ReadDir(rootconfig.ConfigDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read dfmicro config directory: %w", err)
	}

	var clusters []routeCluster
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ",") {
			continue
		}

		cfg, err := rootconfig.ReadClusterConfig(entry.Name())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read cluster config %s: %w", entry.Name(), err)
		}

		containers, err := support.RunningClusterContainers(ctx, r.runner, cfg.Name)
		if err != nil {
			return nil, fmt.Errorf("list running containers for cluster %s: %w", cfg.Name, err)
		}
		if !slices.Contains(containers, cfg.NodeName) {
			continue
		}

		ip, err := support.GetContainerIP(ctx, r.runner, cfg.BridgeName, cfg.NodeName)
		if err != nil {
			return nil, fmt.Errorf("get control node IP for cluster %s: %w", cfg.Name, err)
		}
		clusters = append(clusters, routeCluster{config: cfg, ip: ip})
	}
	return clusters, nil
}

func (r *routeDNS) update(ctx context.Context, cluster routeCluster, blocks []string) error {
	result, err := support.RunPodmanPrivileged(ctx, r.runner,
		"exec", cluster.config.NodeName,
		"oc", "--kubeconfig", internalKubeconfig,
		"get", "configmap", "dns-default", "-n", "openshift-dns",
		"-o=jsonpath={.data.Corefile}",
	)
	if err != nil {
		return fmt.Errorf("get CoreDNS Corefile: %w", err)
	}

	corefile, changed, err := reconcileCorefile(result.Stdout, blocks)
	if err != nil {
		return err
	}
	if !changed {
		r.logger.Info("route DNS is current", "cluster", cluster.config.Name, "peers", len(blocks))
		return nil
	}

	patch, err := json.Marshal(map[string]any{"data": map[string]string{"Corefile": corefile}})
	if err != nil {
		return fmt.Errorf("encode CoreDNS patch: %w", err)
	}
	r.logger.Info("updating route DNS", "cluster", cluster.config.Name, "peers", len(blocks))
	if _, err := support.RunPodmanPrivileged(ctx, r.runner,
		"exec", cluster.config.NodeName,
		"oc", "--kubeconfig", internalKubeconfig,
		"patch", "configmap", "dns-default", "-n", "openshift-dns",
		"--type=merge", "-p", string(patch),
	); err != nil {
		return fmt.Errorf("patch CoreDNS Corefile: %w", err)
	}
	return nil
}

func routeDNSBlock(clusterName, ip string) string {
	zone := "apps." + rootconfig.BaseDomain(clusterName)
	return fmt.Sprintf(`%s:5353 {
	template IN A %s {
		match ^[a-zA-Z0-9-]+\.%s\.$
		answer "{{ .Name }} 30 IN A %s"
	}
}`, zone, zone, regexp.QuoteMeta(zone), ip)
}

func reconcileCorefile(corefile string, blocks []string) (string, bool, error) {
	original := corefile
	start := strings.Index(corefile, routeDNSStart)
	end := strings.Index(corefile, routeDNSEnd)
	if (start == -1) != (end == -1) || (start != -1 && end < start) {
		return "", false, fmt.Errorf("malformed dfmicro route DNS block in Corefile")
	}

	if start != -1 {
		end += len(routeDNSEnd)
		corefile = corefile[:start] + corefile[end:]
	} else if len(blocks) == 0 {
		return corefile, false, nil
	}

	if len(blocks) == 0 {
		return strings.TrimRight(corefile, "\n") + "\n", true, nil
	}

	managed := routeDNSStart + "\n" + strings.Join(blocks, "\n\n") + "\n" + routeDNSEnd
	updated := strings.TrimRight(corefile, "\n") + "\n\n" + managed + "\n"
	return updated, updated != original, nil
}
