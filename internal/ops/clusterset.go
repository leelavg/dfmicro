package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

const (
	volsyncDNSStart       = "# dfmicro volsync begin"
	volsyncDNSEnd         = "# dfmicro volsync end"
	volsyncServicePrefix  = "volsync-rsync-tls-dst-"
	serviceExportsCRDName = "serviceexports.multicluster.x-k8s.io"
)

type clusterSetDNS struct {
	logger *slog.Logger
	runner execx.Runner
}

type volsyncServiceExportList struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	} `json:"items"`
}

type clusterSetDNSRecord struct {
	cluster   string
	bridge    string
	hostname  string
	clusterIP string
}

func clusterSetCommand(dns *clusterSetDNS) *cli.Command {
	return &cli.Command{
		Name:   "clusterset",
		Usage:  "Manage ClusterSet DNS records",
		Action: support.UnknownSubcommand,
		Commands: []*cli.Command{
			{
				Name:  "sync",
				Usage: "Reconcile DNS for exported VolSync destination services",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return dns.reconcile(ctx)
				},
			},
			{
				Name:  "delete",
				Usage: "Remove dfmicro ClusterSet DNS records",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return dns.delete(ctx)
				},
			},
		},
	}
}

func (v *clusterSetDNS) reconcile(ctx context.Context) error {
	clusters, err := runningClusterConfigs(ctx, v.runner)
	if err != nil {
		return err
	}

	var records []clusterSetDNSRecord
	for _, cluster := range clusters {
		clusterRecords, err := v.serviceRecords(ctx, cluster)
		if err != nil {
			return fmt.Errorf("list VolSync exports in cluster %q: %w", cluster.Name, err)
		}
		records = append(records, clusterRecords...)
	}

	var reconcileErrors []error
	for _, cluster := range clusters {
		clusterRecords, err := clusterSetPeerRecords(cluster, records)
		if err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("cluster %s: %w", cluster.Name, err))
			continue
		}

		var blocks []string
		if len(clusterRecords) > 0 {
			blocks = []string{clusterSetDNSBlock(clusterRecords)}
		}
		if err := v.update(ctx, cluster, blocks); err != nil {
			reconcileErrors = append(reconcileErrors, fmt.Errorf("cluster %s: %w", cluster.Name, err))
		}
	}
	return errors.Join(reconcileErrors...)
}

func (v *clusterSetDNS) delete(ctx context.Context) error {
	clusters, err := runningClusterConfigs(ctx, v.runner)
	if err != nil {
		return err
	}

	var deleteErrors []error
	for _, cluster := range clusters {
		if err := v.update(ctx, cluster, nil); err != nil {
			deleteErrors = append(deleteErrors, fmt.Errorf("cluster %s: %w", cluster.Name, err))
		}
	}
	return errors.Join(deleteErrors...)
}

func (v *clusterSetDNS) serviceRecords(ctx context.Context, cluster rootconfig.ClusterConfig) ([]clusterSetDNSRecord, error) {
	crd, err := v.run(ctx, cluster,
		"get", "crd", serviceExportsCRDName, "--ignore-not-found", "-o=name",
	)
	if err != nil {
		return nil, fmt.Errorf("check ServiceExport CRD: %w", err)
	}
	if strings.TrimSpace(crd) == "" {
		return nil, nil
	}

	output, err := v.run(ctx, cluster,
		"get", "serviceexports.multicluster.x-k8s.io", "--all-namespaces", "-o", "json",
	)
	if err != nil {
		return nil, fmt.Errorf("get ServiceExports: %w", err)
	}

	var exports volsyncServiceExportList
	if err := json.Unmarshal([]byte(output), &exports); err != nil {
		return nil, fmt.Errorf("decode ServiceExports: %w", err)
	}

	var records []clusterSetDNSRecord
	for _, export := range exports.Items {
		if !strings.HasPrefix(export.Metadata.Name, volsyncServicePrefix) {
			continue
		}

		clusterIP, err := v.run(ctx, cluster,
			"get", "service", export.Metadata.Name, "-n", export.Metadata.Namespace,
			"--ignore-not-found", "-o=jsonpath={.spec.clusterIP}",
		)
		if err != nil {
			return nil, fmt.Errorf("get service %s/%s: %w", export.Metadata.Namespace, export.Metadata.Name, err)
		}
		clusterIP = strings.TrimSpace(clusterIP)
		if clusterIP == "" || clusterIP == "None" {
			v.logger.Warn("skipping VolSync ServiceExport without a service ClusterIP",
				"cluster", cluster.Name, "namespace", export.Metadata.Namespace, "service", export.Metadata.Name)
			continue
		}

		records = append(records, clusterSetDNSRecord{
			cluster:   cluster.Name,
			bridge:    cluster.BridgeName,
			hostname:  fmt.Sprintf("%s.%s.svc.clusterset.local", export.Metadata.Name, export.Metadata.Namespace),
			clusterIP: clusterIP,
		})
	}
	return records, nil
}

func clusterSetPeerRecords(cluster rootconfig.ClusterConfig, records []clusterSetDNSRecord) ([]clusterSetDNSRecord, error) {
	byHostname := make(map[string]clusterSetDNSRecord)
	for _, record := range records {
		if record.cluster == cluster.Name || record.bridge != cluster.BridgeName {
			continue
		}
		if existing, ok := byHostname[record.hostname]; ok {
			if existing.clusterIP != record.clusterIP {
				return nil, fmt.Errorf("VolSync service %s is exported by multiple peer clusters with different ClusterIPs", record.hostname)
			}
			continue
		}
		byHostname[record.hostname] = record
	}

	peerRecords := make([]clusterSetDNSRecord, 0, len(byHostname))
	for _, record := range byHostname {
		peerRecords = append(peerRecords, record)
	}
	sort.Slice(peerRecords, func(i, j int) bool {
		return peerRecords[i].hostname < peerRecords[j].hostname
	})
	return peerRecords, nil
}

func clusterSetDNSBlock(records []clusterSetDNSRecord) string {
	var hosts []string
	for _, record := range records {
		hosts = append(hosts, fmt.Sprintf("\t\t%s %s", record.clusterIP, record.hostname))
	}
	return fmt.Sprintf(`clusterset.local:5353 {
	hosts {
		ttl 30
%s
		fallthrough
	}
}`, strings.Join(hosts, "\n"))
}

func (v *clusterSetDNS) update(ctx context.Context, cluster rootconfig.ClusterConfig, blocks []string) error {
	corefile, err := v.run(ctx, cluster,
		"get", "configmap", "dns-default", "-n", "openshift-dns", "-o=jsonpath={.data.Corefile}",
	)
	if err != nil {
		return fmt.Errorf("get CoreDNS Corefile: %w", err)
	}

	updated, changed, err := reconcileManagedCorefile(corefile, volsyncDNSStart, volsyncDNSEnd, blocks)
	if err != nil {
		return err
	}
	if !changed {
		v.logger.Info("ClusterSet DNS is current", "cluster", cluster.Name, "records", recordCount(blocks))
		return nil
	}

	patch, err := json.Marshal(map[string]any{"data": map[string]string{"Corefile": updated}})
	if err != nil {
		return fmt.Errorf("encode ClusterSet DNS patch: %w", err)
	}
	v.logger.Info("updating ClusterSet DNS", "cluster", cluster.Name, "records", recordCount(blocks))
	if _, err := v.run(ctx, cluster,
		"patch", "configmap", "dns-default", "-n", "openshift-dns", "--type=merge", "-p", string(patch),
	); err != nil {
		return fmt.Errorf("patch CoreDNS Corefile: %w", err)
	}
	return nil
}

func (v *clusterSetDNS) run(ctx context.Context, cluster rootconfig.ClusterConfig, args ...string) (string, error) {
	command := append([]string{"exec", cluster.NodeName, "oc", "--kubeconfig", internalKubeconfig}, args...)
	result, err := support.RunPodmanPrivileged(ctx, v.runner, command...)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func recordCount(blocks []string) int {
	if len(blocks) == 0 {
		return 0
	}
	return strings.Count(blocks[0], ".svc.clusterset.local")
}
