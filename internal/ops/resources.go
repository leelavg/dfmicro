package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

var defaultName = rootconfig.Load().Name

const (
	internalKubeconfig = "/var/lib/microshift/resources/kubeadmin/kubeconfig"

	podTemplate = `
		{{- range .items -}}
		    {{- $ns := .metadata.namespace -}}
		    {{- $pod := .metadata.name -}}
		    {{- $node := .spec.nodeName -}}
		    {{- range .spec.containers -}}
		        {{- $ns -}}{{"\t"}}
		        {{- $pod -}}{{"\t"}}
		        {{- $node -}}{{"\t"}}
		        {{- .name -}}{{"\t"}}
		        {{- if .resources.requests.cpu }}{{ .resources.requests.cpu }}{{ end }}{{"\t"}}
		        {{- if .resources.requests.memory }}{{ .resources.requests.memory }}{{ end }}{{"\t"}}
		        {{- if .resources.limits.cpu }}{{ .resources.limits.cpu }}{{ end }}{{"\t"}}
		        {{- if .resources.limits.memory }}{{ .resources.limits.memory }}{{ end }}{{"\n"}}
		    {{- end -}}
		{{- end -}}
	`

	nsTruncAt   = 16
	podTruncAt  = 42
	truncSuffix = 3
	dotLen      = 3
)

func resourcesCommand(runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "resources",
		Usage: "Show CPU and memory requests, limits, and live usage per container (experimental)",
		UsageText: `Experimental: output format and flags may change. Use --namespace to scope and improve performance.

Examples:
  dfmicro ops resources
  dfmicro ops resources --namespace openshift-operator-lifecycle-manager
  dfmicro ops resources --name dev --node microshift-node-1`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "name",
				Usage: "Cluster name",
				Value: defaultName,
			},
			&cli.StringFlag{
				Name:  "namespace",
				Usage: "Restrict output to a single namespace (omit for all namespaces)",
			},
			&cli.StringFlag{
				Name:  "node",
				Usage: "Restrict output to a single node by name (omit for all nodes)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			r := &resources{
				runner:    runner,
				cluster:   cmd.String("name"),
				namespace: cmd.String("namespace"),
				node:      cmd.String("node"),
			}
			return r.print(ctx)
		},
	}
}

type resources struct {
	formatter
	runner    execx.Runner
	cluster   string
	namespace string
	node      string
}

type containerRow struct {
	pod, container          string
	cpuReq, memReq          string
	cpuLim, memLim          string
	cpuUseNano, memUseBytes string
}

type nsReport struct {
	name string
	rows []containerRow
}

type nodeReport struct {
	name        string
	capacity    map[string]string
	allocatable map[string]string
	namespaces  []*nsReport
}

type criStatsOutput struct {
	Stats []struct {
		Attributes struct {
			Labels map[string]string `json:"labels"`
		} `json:"attributes"`
		CPU struct {
			UsageNanoCores struct {
				Value string `json:"value"`
			} `json:"usageNanoCores"`
		} `json:"cpu"`
		Memory struct {
			WorkingSetBytes struct {
				Value string `json:"value"`
			} `json:"workingSetBytes"`
		} `json:"memory"`
	} `json:"stats"`
}

type nodeListOutput struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Allocatable map[string]string `json:"allocatable"`
			Capacity    map[string]string `json:"capacity"`
		} `json:"status"`
	} `json:"items"`
}

// looks for a single container for now
func (r *resources) resolveContainer(ctx context.Context) (string, error) {
	result, err := support.RunPodman(ctx, r.runner, "ps",
		"--filter", "label=part-of="+r.cluster,
		"--filter", "label=created-by=dfmicro",
		"--filter", "status=running",
		"--format", "{{.Names}}",
	)
	if err != nil {
		return "", fmt.Errorf("list containers: %w", err)
	}
	name := strings.TrimSpace(result.Stdout)
	if name == "" {
		return "", fmt.Errorf("no running container found for cluster %q", r.cluster)
	}
	return strings.SplitN(name, "\n", 2)[0], nil
}

func (r *resources) podman(ctx context.Context, container string, args ...string) (execx.Result, error) {
	return support.RunPodman(ctx, r.runner, append([]string{"exec", container}, args...)...)
}

func (r *resources) kubectl(ctx context.Context, container string, args ...string) (execx.Result, error) {
	return r.podman(ctx, container, append([]string{"kubectl", "--kubeconfig", internalKubeconfig}, args...)...)
}

func (r *resources) nodes(ctx context.Context, container string) (nodeListOutput, error) {
	result, err := r.kubectl(ctx, container, "get", "nodes", "-o", "json")
	if err != nil {
		return nodeListOutput{}, fmt.Errorf("kubectl get nodes: %w", err)
	}
	var out nodeListOutput
	if err := json.Unmarshal([]byte(result.Stdout), &out); err != nil {
		return nodeListOutput{}, fmt.Errorf("parse nodes: %w", err)
	}
	return out, nil
}

type podRow struct {
	ns, node string
	row      containerRow
}

func (r *resources) podRows(ctx context.Context, container, ns string) ([]podRow, error) {
	args := []string{"get", "pods"}
	if ns != "" {
		args = append(args, "-n", ns)
	} else {
		args = append(args, "-A")
	}
	args = append(args, "-o", "go-template="+podTemplate)
	if r.node != "" {
		args = append(args, "--field-selector", "spec.nodeName="+r.node)
	}
	result, err := r.kubectl(ctx, container, args...)
	if err != nil {
		return nil, fmt.Errorf("kubectl get pods: %w", err)
	}
	var rows []podRow
	for line := range strings.SplitSeq(strings.TrimRight(result.Stdout, "\n"), "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 8 {
			continue
		}
		rows = append(rows, podRow{
			ns:   parts[0],
			node: parts[2],
			row: containerRow{
				pod:       parts[1],
				container: parts[3],
				cpuReq:    parts[4],
				memReq:    parts[5],
				cpuLim:    parts[6],
				memLim:    parts[7],
			},
		})
	}
	return rows, nil
}

// nodeUsageMap fetches stats for all namespaces on a node in parallel via xargs.
// multi-node: grouped already keys by node, so per-node exec is correct for single-node;
// for multi-node clusters each node's containers must be queried via that node's container.
func (r *resources) nodeUsageMap(ctx context.Context, nodeContainer string, namespaces []string) (map[string]containerRow, error) {
	if len(namespaces) == 0 {
		return map[string]containerRow{}, nil
	}
	script := `printf '` + strings.Join(namespaces, `\n`) + `\n' | xargs -P8 -I{} sh -c 'crictl --timeout 30s stats -o json --label "io.kubernetes.pod.namespace=$1" 2>/dev/null; printf "\n---\n"' -- {}`
	result, err := r.podman(ctx, nodeContainer, "sh", "-c", script)
	if err != nil {
		return nil, fmt.Errorf("crictl stats: %w", err)
	}

	m := map[string]containerRow{}
	for chunk := range strings.SplitSeq(result.Stdout, "\n---\n") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		var out criStatsOutput
		if err := json.Unmarshal([]byte(chunk), &out); err != nil {
			continue
		}
		for _, s := range out.Stats {
			ns := s.Attributes.Labels["io.kubernetes.pod.namespace"]
			pod := s.Attributes.Labels["io.kubernetes.pod.name"]
			ctr := s.Attributes.Labels["io.kubernetes.container.name"]
			if ns == "" || pod == "" || ctr == "" {
				continue
			}
			m[ns+"/"+pod+"/"+ctr] = containerRow{
				cpuUseNano:  s.CPU.UsageNanoCores.Value,
				memUseBytes: s.Memory.WorkingSetBytes.Value,
			}
		}
	}
	return m, nil
}

func (r *resources) build(ctx context.Context, targetCtr string) ([]nodeReport, error) {
	nodes, err := r.nodes(ctx, targetCtr)
	if err != nil {
		return nil, err
	}

	nodeMap := map[string]*nodeReport{}
	reports := make([]nodeReport, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		if r.node != "" && n.Metadata.Name != r.node {
			continue
		}
		reports = append(reports, nodeReport{
			name:        n.Metadata.Name,
			capacity:    n.Status.Capacity,
			allocatable: n.Status.Allocatable,
		})
		nodeMap[n.Metadata.Name] = &reports[len(reports)-1]
	}

	type key struct{ node, ns string }
	grouped := map[key][]containerRow{}

	allRows, err := r.podRows(ctx, targetCtr, r.namespace)
	if err != nil {
		return nil, err
	}
	for _, pr := range allRows {
		k := key{pr.node, pr.ns}
		grouped[k] = append(grouped[k], pr.row)
	}

	nodeNamespaces := map[string][]string{}
	for k := range grouped {
		nodeNamespaces[k.node] = append(nodeNamespaces[k.node], k.ns)
	}
	nodeUsage := map[string]map[string]containerRow{}
	for node, nsList := range nodeNamespaces {
		if usage, err := r.nodeUsageMap(ctx, node, nsList); err == nil {
			nodeUsage[node] = usage
		}
	}

	nsIndex := map[key]int{}
	for k, rows := range grouped {
		usage := nodeUsage[k.node]
		for i, row := range rows {
			if u, ok := usage[k.ns+"/"+row.pod+"/"+row.container]; ok {
				rows[i].cpuUseNano = u.cpuUseNano
				rows[i].memUseBytes = u.memUseBytes
			}
		}
		nr, ok := nodeMap[k.node]
		if !ok {
			continue
		}
		idx, seen := nsIndex[k]
		if !seen {
			idx = len(nr.namespaces)
			nsIndex[k] = idx
			nr.namespaces = append(nr.namespaces, &nsReport{name: k.ns})
		}
		nr.namespaces[idx].rows = append(nr.namespaces[idx].rows, rows...)
	}

	for i := range reports {
		sort.Slice(reports[i].namespaces, func(a, b int) bool {
			return reports[i].namespaces[a].name < reports[i].namespaces[b].name
		})
	}
	return reports, nil
}

func (r *resources) print(ctx context.Context) error {
	stop := make(chan struct{})
	go support.Spinner(stop, 300*time.Millisecond)

	targetCtr, err := r.resolveContainer(ctx)
	if err != nil {
		close(stop)
		return err
	}
	reports, err := r.build(ctx, targetCtr)
	close(stop)
	if err != nil {
		return err
	}

	type nsTotals struct {
		pods             int
		cpuReqM, memReqB int64
		cpuLimM, memLimB int64
		cpuUseM, memUseB int64
	}
	nsSummary := map[string]*nsTotals{}

	for _, node := range reports {
		fmt.Printf("\nnode: %s   cpu: %s/%s cores   mem: %s/%s   (allocatable/capacity)\n",
			node.name,
			r.fmtVal(node.allocatable["cpu"]),
			r.fmtVal(node.capacity["cpu"]),
			r.fmtMemRaw(node.allocatable["memory"]),
			r.fmtMemRaw(node.capacity["memory"]),
		)

		var nodeTotalPods int
		var nodeCPUReqM, nodeMemReqB, nodeCPULimM, nodeMemLimB, nodeCPUUseM, nodeMemUseB int64

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "\nNAMESPACE\tPOD\tCONTAINER\tCPU REQ\tMEM REQ\tCPU LIM\tMEM LIM\tCPU USE\tMEM USE")

		for _, ns := range node.namespaces {
			var cpuReqM, memReqB, cpuLimM, memLimB, cpuUseM, memUseB int64
			for _, row := range ns.rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					r.truncName(ns.name, nsTruncAt), r.truncName(row.pod, podTruncAt), row.container,
					r.fmtVal(row.cpuReq), r.fmtVal(row.memReq),
					r.fmtVal(row.cpuLim), r.fmtVal(row.memLim),
					r.fmtCPUUse(row.cpuUseNano, row.cpuReq),
					r.fmtMemUse(row.memUseBytes, row.memReq),
				)
				if v, ok := r.parseCPUMillis(row.cpuReq); ok {
					cpuReqM += v
				}
				if v, ok := r.parseMemBytes(row.memReq); ok {
					memReqB += v
				}
				if v, ok := r.parseCPUMillis(row.cpuLim); ok {
					cpuLimM += v
				}
				if v, ok := r.parseMemBytes(row.memLim); ok {
					memLimB += v
				}
				if row.cpuUseNano != "" {
					if v, err := strconv.ParseInt(row.cpuUseNano, 10, 64); err == nil {
						cpuUseM += v / 1_000_000
					}
				}
				if row.memUseBytes != "" {
					if v, err := strconv.ParseInt(row.memUseBytes, 10, 64); err == nil {
						memUseB += v
					}
				}
			}

			uniquePods := map[string]struct{}{}
			for _, row := range ns.rows {
				uniquePods[row.pod] = struct{}{}
			}
			nsUnique := len(uniquePods)

			nodeTotalPods += nsUnique
			nodeCPUReqM += cpuReqM
			nodeMemReqB += memReqB
			nodeCPULimM += cpuLimM
			nodeMemLimB += memLimB
			nodeCPUUseM += cpuUseM
			nodeMemUseB += memUseB

			if _, ok := nsSummary[ns.name]; !ok {
				nsSummary[ns.name] = &nsTotals{}
			}
			t := nsSummary[ns.name]
			t.pods += nsUnique
			t.cpuReqM += cpuReqM
			t.memReqB += memReqB
			t.cpuLimM += cpuLimM
			t.memLimB += memLimB
			t.cpuUseM += cpuUseM
			t.memUseB += memUseB
		}

		cpuUsePct := r.pctDiff(nodeCPUUseM, nodeCPUReqM)
		memUsePct := r.pctDiff(nodeMemUseB, nodeMemReqB)
		cpuUseStr := fmt.Sprintf("%dm", nodeCPUUseM)
		if cpuUsePct != "" {
			cpuUseStr += " " + cpuUsePct
		}
		memUseStr := r.fmtBytes(nodeMemUseB)
		if memUsePct != "" {
			memUseStr += " " + memUsePct
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			"total", fmt.Sprintf("%d unique pods", nodeTotalPods), "-",
			fmt.Sprintf("%dm", nodeCPUReqM), r.fmtBytes(nodeMemReqB),
			fmt.Sprintf("%dm", nodeCPULimM), r.fmtBytes(nodeMemLimB),
			cpuUseStr, memUseStr,
		)
		w.Flush()
	}

	// namespace summary
	nsNames := make([]string, 0, len(nsSummary))
	for ns := range nsSummary {
		nsNames = append(nsNames, ns)
	}
	sort.Strings(nsNames)

	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAMESPACE\tUNIQUE PODS\tCPU REQ\tMEM REQ\tCPU LIM\tMEM LIM\tCPU USE\tMEM USE")

	var totalPods int
	var totalCPUReqM, totalMemReqB, totalCPULimM, totalMemLimB, totalCPUUseM, totalMemUseB int64
	for _, ns := range nsNames {
		t := nsSummary[ns]
		fmt.Fprintf(w, "%s\t%d\t%dm\t%s\t%dm\t%s\t%dm %s\t%s %s\n",
			ns, t.pods,
			t.cpuReqM, r.fmtBytes(t.memReqB),
			t.cpuLimM, r.fmtBytes(t.memLimB),
			t.cpuUseM, r.pctDiff(t.cpuUseM, t.cpuReqM),
			r.fmtBytes(t.memUseB), r.pctDiff(t.memUseB, t.memReqB),
		)
		totalPods += t.pods
		totalCPUReqM += t.cpuReqM
		totalMemReqB += t.memReqB
		totalCPULimM += t.cpuLimM
		totalMemLimB += t.memLimB
		totalCPUUseM += t.cpuUseM
		totalMemUseB += t.memUseB
	}
	fmt.Fprintf(w, "%s\t%d\t%dm\t%s\t%dm\t%s\t%dm %s\t%s %s\n",
		"total", totalPods,
		totalCPUReqM, r.fmtBytes(totalMemReqB),
		totalCPULimM, r.fmtBytes(totalMemLimB),
		totalCPUUseM, r.pctDiff(totalCPUUseM, totalCPUReqM),
		r.fmtBytes(totalMemUseB), r.pctDiff(totalMemUseB, totalMemReqB),
	)
	return w.Flush()
}

type formatter struct{}

var memSuffixes = [6]struct {
	s    string
	mult int64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30},
	{"K", 1000}, {"M", 1_000_000}, {"G", 1_000_000_000},
}

func (formatter) truncName(s string, at int) string {
	if len(s) <= at+dotLen+truncSuffix {
		return s
	}
	return strings.TrimRight(s[:at], "-._ ") + strings.Repeat(".", dotLen) + s[len(s)-truncSuffix:]
}

func (f formatter) fmtMemRaw(s string) string {
	if s == "" {
		return "-"
	}
	if v, ok := f.parseMemBytes(s); ok {
		return f.fmtBytes(v)
	}
	return s
}

func (formatter) fmtVal(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (formatter) fmtBytes(b int64) string {
	switch {
	case b == 0:
		return "0"
	case b >= 1<<30:
		return fmt.Sprintf("%.1fGi", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%dMi", b>>20)
	default:
		return fmt.Sprintf("%dKi", b>>10)
	}
}

func (formatter) parseCPUMillis(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	if strings.HasSuffix(s, "m") {
		v, err := strconv.ParseInt(strings.TrimSuffix(s, "m"), 10, 64)
		return v, err == nil
	}
	v, err := strconv.ParseFloat(s, 64)
	return int64(v * 1000), err == nil
}

func (formatter) parseMemBytes(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for _, e := range memSuffixes {
		if strings.HasSuffix(s, e.s) {
			v, err := strconv.ParseInt(strings.TrimSuffix(s, e.s), 10, 64)
			return v * e.mult, err == nil
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}

func (formatter) pctDiff(use, req int64) string {
	if req == 0 {
		return ""
	}
	pct := (use - req) * 100 / req
	if pct >= 0 {
		return fmt.Sprintf("(+%d%%)", pct)
	}
	return fmt.Sprintf("(%d%%)", pct)
}

func (f formatter) fmtCPUUse(nanoCores, req string) string {
	if nanoCores == "" {
		return "-"
	}
	v, err := strconv.ParseInt(nanoCores, 10, 64)
	if err != nil || v == 0 {
		return "-"
	}
	use := v / 1_000_000
	s := fmt.Sprintf("%dm", use)
	if reqV, ok := f.parseCPUMillis(req); ok {
		if p := f.pctDiff(use, reqV); p != "" {
			s += " " + p
		}
	}
	return s
}

func (f formatter) fmtMemUse(bytesStr, req string) string {
	if bytesStr == "" {
		return "-"
	}
	v, err := strconv.ParseInt(bytesStr, 10, 64)
	if err != nil {
		return "-"
	}
	s := fmt.Sprintf("%dMi", v>>20)
	if reqV, ok := f.parseMemBytes(req); ok {
		if p := f.pctDiff(v, reqV); p != "" {
			s += " " + p
		}
	}
	return s
}
