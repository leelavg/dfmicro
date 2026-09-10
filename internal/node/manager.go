package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"dfmicro/internal/cluster"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

const (
	multinodeConfigTmpl = `multiNode:
  enabled: true
  controlNodeName: "{{.ControlNodeName}}"
`
	networkConfigTmpl = `network:
  clusterNetwork:
  - {{.ClusterCIDR}}
  serviceNetwork:
  - {{.ServiceCIDR}}
{{- if .Clients}}
  apiServer:
    subjectAltNames:{{range .Clients}}
    - {{.}}{{end}}
{{- end}}
`
	powerTuningConfig = `node:
  powerTuning: true
`
	multusDropinConfig = `[crio.network]
cni_default_network = "multus-cni-network"
plugin_dirs = [
	"/run/cni/bin",
	"/usr/libexec/cni",
]

[crio.runtime]
cni_plugin_dir = [
	"/usr/libexec/cni",
	"/var/lib/microshift/cni",
]
`
)

type manager struct {
	clusterName string
	logger      *slog.Logger
	runner      execx.Runner
}

func newManager(clusterName string, logger *slog.Logger, runner execx.Runner) *manager {
	return &manager{
		clusterName: clusterName,
		logger:      logger,
		runner:      runner,
	}
}

func (m *manager) add(ctx context.Context, force bool, mounts []string) error {
	cfg, err := cluster.ReadClusterConfig(m.clusterName)
	if err != nil {
		return fmt.Errorf("read cluster config: %w", err)
	}

	nodesCfg, err := ReadNodesConfig(m.clusterName)
	if err != nil {
		return fmt.Errorf("read nodes config: %w", err)
	}

	controlNodeName := m.clusterName + "-1"
	if !force {
		if err := m.waitForControlPlane(ctx, cfg, controlNodeName); err != nil {
			return err
		}
	}
	workerIndexes := make([]int, 0, len(nodesCfg.Nodes))
	for _, node := range nodesCfg.Nodes {
		prefix := m.clusterName + "-"
		if !strings.HasPrefix(node.Name, prefix) {
			continue
		}
		number, err := strconv.Atoi(strings.TrimPrefix(node.Name, prefix))
		if err == nil && number >= 2 {
			workerIndexes = append(workerIndexes, number-2)
		}
	}
	nextNodeNum := support.FirstAvailableIndex(workerIndexes, func(index int) int { return index }) + 2
	nodeName := fmt.Sprintf("%s-%d", m.clusterName, nextNodeNum)

	if cfg.EnableTopoLVM && cfg.EnableThinpool {
		nodeDisk := filepath.Join(filepath.Dir(cfg.StateDir), nodeName, nodeName+".image")
		if err := cluster.CreateNodeTopoLVMBackend(ctx, m.runner, nodeDisk, nodeName, cfg.LVMVolSize); err != nil {
			return fmt.Errorf("create node storage: %w", err)
		}
		nodeNames := []string{controlNodeName}
		for _, node := range nodesCfg.Nodes {
			nodeNames = append(nodeNames, node.Name)
		}
		nodeNames = append(nodeNames, nodeName)
		if err := cluster.WriteTopoLVMManifest(cfg, nodeNames); err != nil {
			return fmt.Errorf("write topolvm manifest: %w", err)
		}
	}

	if err := m.extractBootstrapKubeconfig(ctx, cfg, controlNodeName); err != nil {
		return fmt.Errorf("extract bootstrap kubeconfig: %w", err)
	}

	if err := m.extractKubeletCA(ctx, cfg, controlNodeName, nodeName); err != nil {
		return fmt.Errorf("extract kubelet CA: %w", err)
	}

	if err := m.extractCNIConfig(ctx, cfg, controlNodeName, nodeName); err != nil {
		return fmt.Errorf("extract CNI config: %w", err)
	}

	controlNodeIP, err := support.GetContainerIP(ctx, m.runner, cfg.BridgeName, controlNodeName)
	if err != nil {
		return fmt.Errorf("get control node IP: %w", err)
	}

	if err := m.addWorkerNode(ctx, cfg, nodeName, controlNodeName, controlNodeIP, mounts); err != nil {
		return fmt.Errorf("add worker node: %w", err)
	}
	if multiNode, err := cluster.MultiNodeEnabled(m.clusterName); err != nil {
		return fmt.Errorf("read multi-node state: %w", err)
	} else if multiNode {
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "wait", "--for=create", "--timeout=120s", "node/"+nodeName); err != nil {
			return fmt.Errorf("wait for worker node before labeling: %w", err)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "label", "node", nodeName, "dfmicro.io/whereabouts=enabled", "--overwrite"); err != nil {
			return fmt.Errorf("label worker node for whereabouts: %w", err)
		}
	}

	// Open firewall ports for inter-node communication
	if err := m.openKubeletPort(ctx, nodeName); err != nil {
		m.logger.Warn("failed to open kubelet port", "node", nodeName, "error", err)
	}

	nodesCfg.Nodes = append(nodesCfg.Nodes, NodeConfig{
		Name:            nodeName,
		ControlNodeName: controlNodeName,
	})

	if err := WriteNodesConfig(m.clusterName, nodesCfg); err != nil {
		return fmt.Errorf("write nodes config: %w", err)
	}

	m.logger.Info("worker node added", "cluster", m.clusterName, "node", nodeName, "control", controlNodeName)
	return nil
}

func (m *manager) waitForControlPlane(ctx context.Context, cfg cluster.Config, controlNodeName string) error {
	m.logger.Info("waiting for control plane readiness", "node", controlNodeName)
	type resource struct {
		namespace string
		kind      string
		name      string
	}
	resources := []resource{
		{kind: "node", name: controlNodeName},
		{namespace: "openshift-service-ca", kind: "deployment", name: "service-ca"},
		{namespace: "kube-kindnet", kind: "daemonset", name: "kube-kindnet-ds"},
		{namespace: "openshift-multus", kind: "daemonset", name: "multus"},
		{namespace: "openshift-multus", kind: "daemonset", name: "dhcp-daemon"},
		{namespace: "openshift-dns", kind: "daemonset", name: "dns-default"},
	}
	if cfg.EnableTopoLVM && cfg.EnableThinpool {
		resources = append(resources,
			resource{namespace: "topolvm-system", kind: "secret", name: "topolvm-mutatingwebhook"},
			resource{namespace: "topolvm-system", kind: "deployment", name: "topolvm-controller"},
		)
	}
	for _, resource := range resources {
		var args []string
		if resource.kind == "daemonset" {
			args = []string{"exec", controlNodeName, "kubectl", "rollout", "status", "--timeout=120s", "-n", resource.namespace, resource.kind + "/" + resource.name}
		} else {
			condition := "--for=condition=Ready"
			if resource.kind == "deployment" {
				condition = "--for=condition=Available"
			}
			if resource.kind == "secret" {
				condition = "--for=create"
			}
			args = []string{"exec", controlNodeName, "kubectl", "wait", condition, "--timeout=120s"}
			if resource.namespace != "" {
				args = append(args, "-n", resource.namespace)
			}
			args = append(args, resource.kind+"/"+resource.name)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, args...); err != nil {
			return fmt.Errorf("wait for control plane resource %s/%s: %w", resource.kind, resource.name, err)
		}
	}
	m.logger.Info("control plane is ready", "node", controlNodeName)
	return nil
}

func (m *manager) extractBootstrapKubeconfig(ctx context.Context, cfg cluster.Config, controlNodeName string) error {
	bootstrapPath := bootstrapKubeconfigPath(cfg.Name)
	if _, err := os.Stat(bootstrapPath); err == nil {
		return nil
	}

	sourcePath := filepath.Join("/var/lib/microshift/resources/kubeadmin", controlNodeName, "kubeconfig")
	result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", controlNodeName, "cat", sourcePath)
	if err != nil {
		return fmt.Errorf("extract kubeconfig from control node: %w", err)
	}

	if err := os.WriteFile(bootstrapPath, []byte(result.Stdout), 0o644); err != nil {
		return fmt.Errorf("write bootstrap kubeconfig: %w", err)
	}

	return nil
}

func (m *manager) extractKubeletCA(ctx context.Context, cfg cluster.Config, controlNodeName, nodeName string) error {
	workerCertsDir := filepath.Join(filepath.Dir(cfg.StateDir), nodeName, "certs")
	csrSignerDir := filepath.Join(workerCertsDir, "kubelet-csr-signer-signer", "csr-signer")
	caBundlePath := filepath.Join(workerCertsDir, "kubelet-ca.crt")

	if err := os.MkdirAll(csrSignerDir, 0o755); err != nil {
		return fmt.Errorf("create certs dir: %w", err)
	}

	// Keep the signer inputs stable while adding workers to the same cluster.
	for _, file := range []string{"ca.crt", "ca.key", "serial.txt"} {
		targetPath := filepath.Join(csrSignerDir, file)
		if _, err := os.Stat(targetPath); err == nil {
			continue
		}

		sourcePath := filepath.Join("/var/lib/microshift/certs/kubelet-csr-signer-signer/csr-signer", file)
		result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", controlNodeName, "cat", sourcePath)
		if err != nil {
			return fmt.Errorf("extract %s from control node: %w", file, err)
		}

		if err := os.WriteFile(targetPath, []byte(result.Stdout), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", file, err)
		}
	}

	result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", controlNodeName, "cat", "/var/lib/microshift/certs/ca-bundle/kubelet-ca.crt")
	if err != nil {
		return fmt.Errorf("extract kubelet CA bundle from control node: %w", err)
	}
	clientCA, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", controlNodeName, "cat", "/var/lib/microshift/certs/kube-apiserver-to-kubelet-client-signer/ca.crt")
	if err != nil {
		return fmt.Errorf("extract kube-apiserver-to-kubelet client CA: %w", err)
	}
	if !strings.Contains(result.Stdout, clientCA.Stdout) {
		result.Stdout += clientCA.Stdout
	}
	if err := os.WriteFile(caBundlePath, []byte(result.Stdout), 0o644); err != nil {
		return fmt.Errorf("write kubelet CA bundle: %w", err)
	}

	// Always ensure kubelet-server dir exists so worker can create serving certs
	kubeletServerDir := filepath.Join(csrSignerDir, "kubelet-server")
	if err := os.MkdirAll(kubeletServerDir, 0o755); err != nil {
		return fmt.Errorf("create kubelet-server dir: %w", err)
	}

	return nil
}

func (m *manager) openKubeletPort(ctx context.Context, nodeName string) error {
	var lastErr error
	for range 30 {
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", nodeName, "firewall-cmd", "--zone=public", "--add-port=10250/tcp"); err == nil {
			return nil
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return fmt.Errorf("open kubelet port after retries: %w", lastErr)
}

func (m *manager) extractCNIConfig(ctx context.Context, cfg cluster.Config, controlNodeName, nodeName string) error {
	cniPath := filepath.Join(filepath.Dir(cfg.StateDir), nodeName, "cni", "10-kindnet.conflist")

	if err := os.MkdirAll(filepath.Dir(cniPath), 0o755); err != nil {
		return fmt.Errorf("create cni dir: %w", err)
	}

	sourcePath := "/etc/cni/net.d/10-kindnet.conflist"
	var cniConfig string
	var lastErr error
	for range 30 {
		result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", controlNodeName, "cat", sourcePath)
		if err == nil {
			cniConfig = result.Stdout
			break
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	if cniConfig == "" {
		if lastErr == nil {
			return fmt.Errorf("extract CNI config from control node: empty config")
		}
		return fmt.Errorf("extract CNI config from control node after retries: %w", lastErr)
	}

	workerCIDR, err := workerPodCIDR(cfg.ClusterCIDR, nodeName)
	if err != nil {
		return fmt.Errorf("calculate pod CIDR for %s: %w", nodeName, err)
	}
	cniData, err := rewriteCNISubnet([]byte(cniConfig), workerCIDR)
	if err != nil {
		return fmt.Errorf("rewrite CNI config for %s: %w", nodeName, err)
	}

	if err := os.WriteFile(cniPath, cniData, 0o644); err != nil {
		return fmt.Errorf("write CNI config: %w", err)
	}

	return nil
}

func workerPodCIDR(clusterCIDR, nodeName string) (string, error) {
	prefix, err := netip.ParsePrefix(clusterCIDR)
	if err != nil {
		return "", err
	}
	if prefix.Addr().Is6() || prefix.Bits() > 24 {
		return "", fmt.Errorf("IPv4 cluster CIDR with prefix <= 24 required, got %s", clusterCIDR)
	}

	nodeNumber, err := strconv.Atoi(nodeName[strings.LastIndexByte(nodeName, '-')+1:])
	if err != nil || nodeNumber < 2 {
		return "", fmt.Errorf("invalid worker node name %q", nodeName)
	}

	base := prefix.Masked().Addr().As4()
	baseValue := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	blockCount := uint32(1) << uint(24-prefix.Bits())
	blockIndex := uint32(nodeNumber - 1)
	if blockIndex >= blockCount {
		return "", fmt.Errorf("node number %d is outside cluster CIDR %s", nodeNumber, clusterCIDR)
	}
	workerValue := baseValue + (blockIndex << 8)
	workerAddr := netip.AddrFrom4([4]byte{byte(workerValue >> 24), byte(workerValue >> 16), byte(workerValue >> 8), byte(workerValue)})
	return workerAddr.String() + "/24", nil
}

func rewriteCNISubnet(data []byte, subnet string) ([]byte, error) {
	var config any
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	var rewrite func(any)
	rewrite = func(value any) {
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if key == "subnet" {
					value[key] = subnet
					continue
				}
				rewrite(child)
			}
		case []any:
			for _, child := range value {
				rewrite(child)
			}
		}
	}
	rewrite(config)

	return json.MarshalIndent(config, "", "\t")
}

func (m *manager) addWorkerNode(ctx context.Context, cfg cluster.Config, nodeName, controlNodeName, controlNodeIP string, mounts []string) error {
	multinodeConfigData := struct {
		ControlNodeName string
	}{
		ControlNodeName: controlNodeName,
	}

	var multinodeBuf bytes.Buffer
	tmpl := template.Must(template.New("").Parse(multinodeConfigTmpl))
	if err := tmpl.Execute(&multinodeBuf, multinodeConfigData); err != nil {
		return err
	}

	multinodePath := filepath.Join(cfg.StateDir, "20-multinode.yaml")
	if err := os.WriteFile(multinodePath, multinodeBuf.Bytes(), 0o644); err != nil {
		return err
	}

	bootstrapSource := bootstrapKubeconfigPath(cfg.Name)
	bootstrapTarget := "/var/lib/microshift/bootstrap/kubeconfig"

	// Mount control plane's kubelet CA (not serving cert) so worker generates own cert signed by same CA
	csrSignerDir := filepath.Join(filepath.Dir(cfg.StateDir), nodeName, "certs", "kubelet-csr-signer-signer", "csr-signer")
	csrSignerTarget := "/var/lib/microshift/certs/kubelet-csr-signer-signer/csr-signer"
	caBundleFile := filepath.Join(filepath.Dir(cfg.StateDir), nodeName, "certs", "kubelet-ca.crt")
	caBundleTarget := "/var/lib/microshift/certs/ca-bundle/kubelet-ca.crt"
	cniSource := filepath.Join(filepath.Dir(cfg.StateDir), nodeName, "cni", "10-kindnet.conflist")
	cniTarget := "/etc/cni/net.d/10-kindnet.conflist"

	args := []string{
		"podman", "run", "--privileged", "-d",
		"--ulimit", "nofile=524288:524288",
		"--tty",
		"--volume", "/dev:/dev",
	}

	if cfg.ShareHostContainers {
		args = append(args, "--volume", "/var/lib/containers:/var/lib/containers")
	}

	for _, device := range []string{"input", "snd", "dri"} {
		if info, err := os.Stat(filepath.Join("/dev", device)); err == nil && info.IsDir() {
			args = append(args, "--tmpfs", filepath.Join("/dev", device))
		}
	}

	args = append(args,
		"--network", cfg.BridgeName,
		"--dns-search=.",
		"--add-host", controlNodeName+":"+controlNodeIP,
	)

	if !cfg.EnableTopoLVM {
		emptyTopoLVMDir := filepath.Join(cfg.StateDir, "empty-topolvm")
		if err := os.MkdirAll(emptyTopoLVMDir, 0o755); err != nil {
			return err
		}
		args = append(args,
			"--volume", emptyTopoLVMDir+":/usr/lib/microshift/manifests.d/001-microshift-topolvm:ro",
		)
	}
	if cfg.EnableTopoLVM && cfg.EnableThinpool {
		args = append(args,
			"--volume", filepath.Join(cfg.StateDir, "kustomization.yaml")+":/usr/lib/microshift/manifests.d/001-microshift-topolvm/kustomization.yaml:ro",
			"--volume", cluster.TopoLVMManifestPath(cfg)+":/usr/lib/microshift/manifests.d/001-microshift-topolvm/04-dfmicro-topolvm.yaml:ro",
			"--volume", filepath.Join(cfg.StateDir, "dfmicro-topolvm-patch.yaml")+":/usr/lib/microshift/manifests.d/001-microshift-topolvm/dfmicro-topolvm-patch.yaml:ro",
		)
	}

	networkData := struct {
		Clients     []string
		ClusterCIDR string
		ServiceCIDR string
	}{
		Clients:     nil,
		ClusterCIDR: cfg.ClusterCIDR,
		ServiceCIDR: cfg.ServiceCIDR,
	}

	var networkBuf bytes.Buffer
	networkTmpl := template.Must(template.New("").Parse(networkConfigTmpl))
	if err := networkTmpl.Execute(&networkBuf, networkData); err != nil {
		return err
	}

	networkPath := filepath.Join(cfg.StateDir, nodeName+"-15-networking.yaml")
	if err := os.WriteFile(networkPath, networkBuf.Bytes(), 0o644); err != nil {
		return err
	}
	args = append(args,
		"--volume", networkPath+":/etc/microshift/config.d/15-networking.yaml:ro",
		"--volume", multinodePath+":/etc/microshift/config.d/20-multinode.yaml:ro",
		"--volume", bootstrapSource+":"+bootstrapTarget+":ro",
		"--volume", bootstrapSource+":/var/lib/microshift/resources/kubeadmin/kubeconfig",
		"--volume", csrSignerDir+":"+csrSignerTarget,
		"--volume", caBundleFile+":"+caBundleTarget+":ro",
		"--volume", cniSource+":"+cniTarget+":ro",
	)

	if cfg.PowerTuning {
		powerTuningPath := filepath.Join(cfg.StateDir, "power-tuning.yaml")
		if err := os.WriteFile(powerTuningPath, []byte(powerTuningConfig), 0o644); err != nil {
			return err
		}
		args = append(args, "--volume", powerTuningPath+":/etc/microshift/config.d/10-power-tuning.yaml:ro")
	}

	if cfg.PullSecret != "" {
		args = append(args, "--volume", cfg.PullSecret+":/etc/crio/openshift-pull-secret:ro")
	}

	crioDropinPath := filepath.Join(cfg.StateDir, "20-multus-cni-plugins.conf")
	if err := os.WriteFile(crioDropinPath, []byte(multusDropinConfig), 0o644); err != nil {
		return err
	}
	args = append(args, "--volume", crioDropinPath+":/etc/crio/crio.conf.d/20-multus-cni-plugins.conf:ro")

	if len(cfg.IDMSFiles) > 0 {
		result, err := support.ConvertIDMSFiles(cfg.IDMSFiles)
		if err != nil {
			return err
		}
		mirrorsPath := filepath.Join(cfg.StateDir, "99-mirrors.conf")
		if err := os.WriteFile(mirrorsPath, []byte(result.RegistriesConf), 0o644); err != nil {
			return err
		}
		policyPath := filepath.Join(cfg.StateDir, "policy.json")
		if err := os.WriteFile(policyPath, []byte(result.PolicyJSON), 0o644); err != nil {
			return err
		}
		args = append(args,
			"--volume", mirrorsPath+":/etc/containers/registries.conf.d/99-mirrors.conf:ro",
			"--volume", policyPath+":/etc/containers/policy.json:ro",
		)
	}

	for _, mount := range mounts {
		args = append(args, "--volume", mount)
	}

	args = append(args,
		"--label", "part-of="+cfg.Name,
		"--label", "created-by=dfmicro",
		"--name", nodeName,
		"--hostname", nodeName,
		cfg.Image,
	)

	m.logger.Info("starting worker node", "name", nodeName, "image", cfg.Image)
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, args[1:]...); err != nil {
		return err
	}
	apiIP, err := apiServerIP(cfg.ServiceCIDR)
	if err != nil {
		return fmt.Errorf("calculate API server IP: %w", err)
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", nodeName, "ip", "route", "replace", apiIP+"/32", "via", controlNodeIP, "dev", "eth0"); err != nil {
		return fmt.Errorf("route API server IP through control node: %w", err)
	}
	if cfg.EnableTopoLVM && cfg.EnableThinpool {
		m.logger.Info("waiting for worker readiness", "node", nodeName)
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "wait", "--for=create", "--timeout=120s", "node/"+nodeName); err != nil {
			return fmt.Errorf("wait for worker registration: %w", err)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "wait", "--for=condition=Ready", "--timeout=120s", "node/"+nodeName); err != nil {
			return fmt.Errorf("wait for worker readiness: %w", err)
		}
		m.logger.Info("worker is ready", "node", nodeName)
		m.logger.Info("waiting for service-ca", "node", nodeName)
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "wait", "--for=condition=Available", "--timeout=120s", "-n", "openshift-service-ca", "deployment/service-ca"); err != nil {
			return fmt.Errorf("wait for service-ca: %w", err)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "apply", "-k", "/usr/lib/microshift/manifests.d/001-microshift-topolvm"); err != nil {
			return fmt.Errorf("apply topolvm manifest: %w", err)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "wait", "--for=create", "--timeout=120s", "-n", "topolvm-system", "secret/topolvm-mutatingwebhook"); err != nil {
			return fmt.Errorf("wait for topolvm webhook secret: %w", err)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "wait", "--for=condition=Available", "--timeout=120s", "-n", "topolvm-system", "deployment/topolvm-controller"); err != nil {
			return fmt.Errorf("wait for topolvm controller: %w", err)
		}
	}

	return nil
}

func apiServerIP(serviceCIDR string) (string, error) {
	prefix, err := netip.ParsePrefix(serviceCIDR)
	if err != nil {
		return "", err
	}
	if prefix.Addr().Is6() {
		return "", fmt.Errorf("IPv6 service CIDR is not supported")
	}
	base := prefix.Masked().Addr().As4()
	hostBits := 32 - prefix.Bits()
	if hostBits == 0 {
		return "", fmt.Errorf("service CIDR has no next subnet: %s", serviceCIDR)
	}
	step := uint32(1) << hostBits
	value := uint32(base[0])<<24 | uint32(base[1])<<16 | uint32(base[2])<<8 | uint32(base[3])
	if value > ^uint32(0)-step {
		return "", fmt.Errorf("service CIDR has no next subnet: %s", serviceCIDR)
	}
	value += step
	return netip.AddrFrom4([4]byte{byte(value >> 24), byte(value >> 16), byte(value >> 8), byte(value)}).String(), nil
}

func (m *manager) remove(ctx context.Context, nodeName string) error {
	cfg, err := cluster.ReadClusterConfig(m.clusterName)
	if err != nil {
		return fmt.Errorf("read cluster config: %w", err)
	}
	nodesCfg, err := ReadNodesConfig(m.clusterName)
	if err != nil {
		return fmt.Errorf("read nodes config: %w", err)
	}
	found := false
	for _, node := range nodesCfg.Nodes {
		if node.Name == nodeName {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("worker node %q not found in cluster %q", nodeName, m.clusterName)
	}

	controlNodeName := m.clusterName + "-1"
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "delete", "node", nodeName, "--ignore-not-found", "--wait=false"); err != nil {
		m.logger.Warn("failed to delete Kubernetes node", "name", nodeName, "error", err)
	}
	if cfg.EnableTopoLVM && cfg.EnableThinpool {
		if index, ok := nodeIndex(nodeName); ok {
			if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "-n", "topolvm-system", "delete", "daemonset", fmt.Sprintf("topolvm-lvmd-%d", index-1), "configmap", fmt.Sprintf("topolvm-lvmd-%d", index-1), "--ignore-not-found"); err != nil {
				m.logger.Warn("failed to delete TopoLVM node resources", "name", nodeName, "error", err)
			}
		}
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "stop", "--time", "3", nodeName); err != nil {
		m.logger.Warn("failed to stop node container", "name", nodeName, "error", err)
	}

	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "rm", "-f", "--volumes", nodeName); err != nil {
		return fmt.Errorf("failed to remove node container: %w", err)
	}

	for i, node := range nodesCfg.Nodes {
		if node.Name == nodeName {
			nodesCfg.Nodes = append(nodesCfg.Nodes[:i], nodesCfg.Nodes[i+1:]...)
			break
		}
	}
	if cfg.EnableTopoLVM && cfg.EnableThinpool {
		nodeDisk := filepath.Join(filepath.Dir(cfg.StateDir), nodeName, nodeName+".image")
		if err := cluster.DeleteNodeTopoLVMBackend(ctx, m.runner, nodeDisk, nodeName); err != nil {
			return fmt.Errorf("delete node storage: %w", err)
		}
	} else if err := os.RemoveAll(filepath.Join(filepath.Dir(cfg.StateDir), nodeName)); err != nil {
		return fmt.Errorf("remove node state: %w", err)
	}
	if cfg.EnableTopoLVM && cfg.EnableThinpool {
		nodeNames := []string{controlNodeName}
		for _, node := range nodesCfg.Nodes {
			nodeNames = append(nodeNames, node.Name)
		}
		if err := cluster.WriteTopoLVMManifest(cfg, nodeNames); err != nil {
			return fmt.Errorf("write topolvm manifest: %w", err)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "apply", "-k", "/usr/lib/microshift/manifests.d/001-microshift-topolvm"); err != nil {
			m.logger.Warn("failed to apply TopoLVM manifest", "error", err)
		}
	}

	if err := WriteNodesConfig(m.clusterName, nodesCfg); err != nil {
		return fmt.Errorf("write nodes config: %w", err)
	}

	m.logger.Info("worker node deleted", "cluster", m.clusterName, "node", nodeName)
	return nil
}

func nodeIndex(name string) (int, bool) {
	index := strings.LastIndexByte(name, '-')
	if index < 0 {
		return 0, false
	}
	number, err := strconv.Atoi(name[index+1:])
	return number, err == nil && number >= 2
}
