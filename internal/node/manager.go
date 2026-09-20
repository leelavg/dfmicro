package node

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"text/template"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/network"
	"dfmicro/internal/support"
)

type manager struct {
	clusterName string
	cfg         rootconfig.ClusterConfig
	node        *support.NodeMgr
	topolvm     *support.TopoLVMMgr
	logger      *slog.Logger
	runner      execx.Runner
}

type worker struct {
	index           int
	name            string
	stateDir        string
	controlNodeName string
	controlNodeIP   string
	mounts          []string
}

func newManager(clusterName string, logger *slog.Logger, runner execx.Runner) (*manager, error) {
	cfg, err := rootconfig.ReadClusterConfig(clusterName)
	if err != nil {
		return nil, fmt.Errorf("read cluster config: %w", err)
	}
	return &manager{
		clusterName: clusterName,
		cfg:         cfg,
		node:        support.NewNodeMgr(logger, runner),
		topolvm:     support.NewTopoLVMMgr(runner, cfg),
		logger:      logger,
		runner:      runner,
	}, nil
}

func (m *manager) add(ctx context.Context, force bool, mounts []string) error {
	nodesCfg, err := readNodesConfig(m.clusterName)
	if err != nil {
		return fmt.Errorf("read nodes config: %w", err)
	}

	controlNodeName, err := controlNode(nodesCfg)
	if err != nil {
		return err
	}
	if !force {
		if err := m.waitForControlPlane(ctx, controlNodeName); err != nil {
			return err
		}
	}
	if err := network.ValidateWorkerAdd(m.clusterName); err != nil {
		return err
	}
	worker, err := m.prepareWorker(ctx, nodesCfg, controlNodeName, mounts)
	if err != nil {
		return err
	}
	if err := m.createWorker(ctx, worker); err != nil {
		return fmt.Errorf("add worker node: %w", err)
	}
	if err := m.reconcileWorker(ctx, worker); err != nil {
		return err
	}
	if err := m.persistWorker(nodesCfg, worker); err != nil {
		return fmt.Errorf("write nodes config: %w", err)
	}

	m.logger.Info("worker node added", "cluster", m.clusterName, "node", worker.name, "control", worker.controlNodeName)
	return nil
}

func controlNode(nodesCfg rootconfig.NodesConfig) (string, error) {
	if len(nodesCfg.Nodes) == 0 || nodesCfg.Nodes[0].Index != 0 {
		return "", fmt.Errorf("cluster has no control node in nodes.json")
	}
	return nodesCfg.Nodes[0].NodeName, nil
}

func (m *manager) prepareWorker(ctx context.Context, nodesCfg rootconfig.NodesConfig, controlNodeName string, mounts []string) (worker, error) {
	index := support.FirstAvailableIndex(nodesCfg.Nodes, func(node rootconfig.NodeConfig) int {
		return node.Index
	})
	name := rootconfig.NodeName(m.clusterName, index)
	stateDir := filepath.Join(m.cfg.StateDir, name)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return worker{}, fmt.Errorf("create node state directory: %w", err)
	}
	if err := m.topolvm.CreateBackend(ctx, name); err != nil {
		return worker{}, fmt.Errorf("create node storage: %w", err)
	}
	if err := m.extractBootstrapKubeconfig(ctx, m.cfg, controlNodeName); err != nil {
		return worker{}, fmt.Errorf("extract bootstrap kubeconfig: %w", err)
	}
	if err := m.extractKubeletCA(ctx, m.cfg, controlNodeName, name); err != nil {
		return worker{}, fmt.Errorf("extract kubelet CA: %w", err)
	}
	controlIP, err := support.GetContainerIP(ctx, m.runner, m.cfg.BridgeName, controlNodeName)
	if err != nil {
		return worker{}, fmt.Errorf("get control node IP: %w", err)
	}
	return worker{
		index:           index,
		name:            name,
		stateDir:        stateDir,
		controlNodeName: controlNodeName,
		controlNodeIP:   controlIP,
		mounts:          mounts,
	}, nil
}

func (m *manager) reconcileWorker(ctx context.Context, w worker) error {
	if err := m.topolvm.Reconcile(ctx, m.clusterName, w.controlNodeName); err != nil {
		return fmt.Errorf("apply topolvm manifest: %w", err)
	}
	multiNode, err := network.UsesWhereabouts(m.clusterName)
	if err != nil {
		return fmt.Errorf("read multi-node state: %w", err)
	}
	if !multiNode {
		return nil
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", w.controlNodeName, "kubectl", "wait", "--for=create", "--timeout=120s", "node/"+w.name); err != nil {
		return fmt.Errorf("wait for worker node before labeling: %w", err)
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", w.controlNodeName, "kubectl", "label", "node", w.name, "dfmicro.io/whereabouts=enabled", "--overwrite"); err != nil {
		return fmt.Errorf("label worker node for whereabouts: %w", err)
	}
	return nil
}

func (m *manager) persistWorker(nodesCfg rootconfig.NodesConfig, w worker) error {
	nodesCfg.Nodes = slices.Insert(nodesCfg.Nodes, w.index, rootconfig.NodeConfig{
		Index:      w.index,
		NodeName:   w.name,
		LVMDisk:    filepath.Join(w.stateDir, w.name+".image"),
		VGName:     w.name,
		PullSecret: m.cfg.PullSecret,
		IDMSFiles:  append([]string(nil), m.cfg.IDMSFiles...),
		Mounts:     append([]string(nil), w.mounts...),
	})
	return writeNodesConfig(m.clusterName, nodesCfg)
}

func (m *manager) waitForControlPlane(ctx context.Context, controlNodeName string) error {
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
	// TODO: reduce bootstrap exec calls once worker startup has a stable API.
	for _, resource := range resources {
		var args []string
		if resource.kind == "daemonset" {
			args = []string{"exec", controlNodeName, "kubectl", "rollout", "status", "--timeout=120s", "-n", resource.namespace, resource.kind + "/" + resource.name}
		} else {
			condition := "--for=condition=Ready"
			if resource.kind == "deployment" {
				condition = "--for=condition=Available"
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

func (m *manager) extractBootstrapKubeconfig(ctx context.Context, cfg rootconfig.ClusterConfig, controlNodeName string) error {
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

func (m *manager) extractKubeletCA(ctx context.Context, cfg rootconfig.ClusterConfig, controlNodeName, nodeName string) error {
	workerCertsDir := filepath.Join(cfg.StateDir, nodeName, "certs")
	csrSignerDir := filepath.Join(workerCertsDir, "kubelet-csr-signer-signer", "csr-signer")
	caBundlePath := filepath.Join(workerCertsDir, "kubelet-ca.crt")

	if err := os.MkdirAll(csrSignerDir, 0o755); err != nil {
		return fmt.Errorf("create certs dir: %w", err)
	}

	result, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", "-i", controlNodeName, "sh", "-c", `
set -e
for file in ca.crt ca.key serial.txt; do
    printf '\nDFMICRO_FILE:%s\n' "$file"
    cat "/var/lib/microshift/certs/kubelet-csr-signer-signer/csr-signer/$file"
done
printf '\nDFMICRO_FILE:kubelet-ca.crt\n'
cat /var/lib/microshift/certs/ca-bundle/kubelet-ca.crt
printf '\nDFMICRO_FILE:client-ca.crt\n'
cat /var/lib/microshift/certs/kube-apiserver-to-kubelet-client-signer/ca.crt
`)
	if err != nil {
		return fmt.Errorf("extract kubelet certificates from control node: %w", err)
	}

	files := make(map[string]string, 5)
	for _, section := range strings.Split(result.Stdout, "DFMICRO_FILE:")[1:] {
		before, after, ok := strings.Cut(section, "\n")
		if !ok {
			return fmt.Errorf("parse kubelet certificate output")
		}
		files[strings.TrimSpace(before)] = after
	}

	// Keep the signer inputs stable while adding workers to the same cluster.
	for _, file := range []string{"ca.crt", "ca.key", "serial.txt"} {
		targetPath := filepath.Join(csrSignerDir, file)
		if _, err := os.Stat(targetPath); err == nil {
			continue
		}
		if err := os.WriteFile(targetPath, []byte(files[file]), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", file, err)
		}
	}

	caBundle := files["kubelet-ca.crt"]
	clientCA := files["client-ca.crt"]
	if !strings.Contains(caBundle, clientCA) {
		caBundle += clientCA
	}
	if err := os.WriteFile(caBundlePath, []byte(caBundle), 0o644); err != nil {
		return fmt.Errorf("write kubelet CA bundle: %w", err)
	}

	// Always ensure kubelet-server dir exists so worker can create serving certs
	kubeletServerDir := filepath.Join(csrSignerDir, "kubelet-server")
	if err := os.MkdirAll(kubeletServerDir, 0o755); err != nil {
		return fmt.Errorf("create kubelet-server dir: %w", err)
	}

	return nil
}

func (m *manager) createWorker(ctx context.Context, w worker) error {
	extraArgs, err := m.workerArgs(w)
	if err != nil {
		return err
	}
	if err := m.node.Create(ctx, support.NodeSpec{
		Name:                w.name,
		Image:               m.cfg.Image,
		Network:             m.cfg.BridgeName,
		StateDir:            w.stateDir,
		ClusterName:         m.cfg.Name,
		ShareHostContainers: m.cfg.ShareHostContainers,
		PullSecret:          m.cfg.PullSecret,
		IDMSFiles:           m.cfg.IDMSFiles,
		Mounts:              w.mounts,
		ExtraArgs:           extraArgs,
		Trust:               m.networkTrust(),
	}); err != nil {
		return fmt.Errorf("create worker node: %w", err)
	}
	if err := m.configureWorkerNetwork(ctx, w); err != nil {
		return err
	}
	return m.waitForWorker(ctx, w)
}

func (m *manager) workerArgs(w worker) ([]string, error) {
	nodeStateDir := w.stateDir
	multinodeConfigData := struct {
		ControlNodeName string
	}{
		ControlNodeName: w.controlNodeName,
	}

	var multinodeBuf bytes.Buffer
	tmpl := template.Must(template.New("").Parse(multinodeConfigTmpl))
	if err := tmpl.Execute(&multinodeBuf, multinodeConfigData); err != nil {
		return nil, err
	}

	multinodePath := filepath.Join(nodeStateDir, "20-multinode.yaml")
	if err := os.WriteFile(multinodePath, multinodeBuf.Bytes(), 0o644); err != nil {
		return nil, err
	}

	bootstrapSource := bootstrapKubeconfigPath(m.cfg.Name)
	bootstrapTarget := "/var/lib/microshift/bootstrap/kubeconfig"

	// Mount control plane's kubelet CA (not serving cert) so worker generates own cert signed by same CA
	csrSignerDir := filepath.Join(nodeStateDir, "certs", "kubelet-csr-signer-signer", "csr-signer")
	csrSignerTarget := "/var/lib/microshift/certs/kubelet-csr-signer-signer/csr-signer"
	caBundleFile := filepath.Join(nodeStateDir, "certs", "kubelet-ca.crt")
	caBundleTarget := "/var/lib/microshift/certs/ca-bundle/kubelet-ca.crt"
	extraArgs := []string{
		"--add-host", w.controlNodeName + ":" + w.controlNodeIP,
	}

	networkData := struct {
		Clients     []string
		ClusterCIDR string
		ServiceCIDR string
		BaseDomain  string
	}{
		Clients:     nil,
		ClusterCIDR: m.cfg.ClusterCIDR,
		ServiceCIDR: m.cfg.ServiceCIDR,
		BaseDomain:  m.cfg.Name + ".dfmicro.io",
	}

	networkPath := filepath.Join(nodeStateDir, "15-networking.yaml")
	if err := support.WriteNetworkConfig(networkPath, networkData.BaseDomain, networkData.ClusterCIDR, networkData.ServiceCIDR, networkData.Clients); err != nil {
		return nil, err
	}
	extraArgs = append(extraArgs,
		"--volume", networkPath+":/etc/microshift/config.d/15-networking.yaml:ro",
		"--volume", multinodePath+":/etc/microshift/config.d/20-multinode.yaml:ro",
		"--volume", bootstrapSource+":"+bootstrapTarget+":ro",
		"--volume", bootstrapSource+":/var/lib/microshift/resources/kubeadmin/kubeconfig",
		"--volume", csrSignerDir+":"+csrSignerTarget,
		"--volume", caBundleFile+":"+caBundleTarget+":ro",
	)

	if m.cfg.PowerTuning {
		powerTuningPath := filepath.Join(nodeStateDir, "power-tuning.yaml")
		if err := support.WritePowerTuningConfig(powerTuningPath); err != nil {
			return nil, err
		}
		extraArgs = append(extraArgs, "--volume", powerTuningPath+":/etc/microshift/config.d/10-power-tuning.yaml:ro")
	}

	crioDropinPath := filepath.Join(nodeStateDir, "20-multus-cni-plugins.conf")
	if err := support.WriteMultusDropin(crioDropinPath); err != nil {
		return nil, err
	}
	extraArgs = append(extraArgs, "--volume", crioDropinPath+":/etc/crio/crio.conf.d/20-multus-cni-plugins.conf:ro")

	return extraArgs, nil
}

func (m *manager) networkTrust() support.NetworkTrust {
	return support.NetworkTrust{
		Sources:    []string{m.cfg.ClusterCIDR, m.cfg.ServiceCIDR, m.cfg.BridgeSubnet},
		Interfaces: []string{"eth0"},
	}
}

func (m *manager) configureWorkerNetwork(ctx context.Context, w worker) error {
	apiIP, err := apiServerIP(m.cfg.ServiceCIDR)
	if err != nil {
		return fmt.Errorf("calculate API server IP: %w", err)
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", w.name, "ip", "route", "replace", apiIP+"/32", "via", w.controlNodeIP, "dev", "eth0"); err != nil {
		return fmt.Errorf("route API server IP through control node: %w", err)
	}
	return nil
}

func (m *manager) waitForWorker(ctx context.Context, w worker) error {
	m.logger.Info("waiting for worker readiness", "node", w.name)
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", w.controlNodeName, "kubectl", "wait", "--for=create", "--timeout=120s", "node/"+w.name); err != nil {
		return fmt.Errorf("wait for worker registration: %w", err)
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", w.controlNodeName, "kubectl", "wait", "--for=condition=Ready", "--timeout=120s", "node/"+w.name); err != nil {
		return fmt.Errorf("wait for worker readiness: %w", err)
	}
	if err := m.node.WaitReady(ctx, w.name); err != nil {
		return fmt.Errorf("wait for worker node readiness: %w", err)
	}
	m.logger.Info("worker is ready", "node", w.name)
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
	nodesCfg, err := readNodesConfig(m.clusterName)
	if err != nil {
		return fmt.Errorf("read nodes config: %w", err)
	}
	if !strings.HasPrefix(nodeName, m.clusterName+"-") {
		return fmt.Errorf("node %q does not belong to cluster %q", nodeName, m.clusterName)
	}
	if _, ok := nodeIndex(nodeName); !ok {
		return fmt.Errorf("node %q is not a worker node", nodeName)
	}
	controlNodeName, err := controlNode(nodesCfg)
	if err != nil {
		return err
	}

	cleanupErrs := []error{
		m.removeWorkerResources(ctx, controlNodeName, nodeName),
		m.removeWorkerContainer(ctx, nodeName),
		m.removeWorkerStorage(ctx, nodeName),
	}
	nodesCfg = removeWorker(nodesCfg, nodeName)
	cleanupErrs = append(cleanupErrs, m.persistNodeRemoval(nodesCfg))
	cleanupErrs = append(cleanupErrs, m.topolvm.Reconcile(ctx, m.clusterName, controlNodeName))

	m.logger.Info("worker node deleted", "cluster", m.clusterName, "node", nodeName)
	return errors.Join(cleanupErrs...)
}

func (m *manager) removeWorkerResources(ctx context.Context, controlNodeName, nodeName string) error {
	var errs []error
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "delete", "node", nodeName, "--ignore-not-found", "--wait=false"); err != nil {
		errs = append(errs, fmt.Errorf("delete Kubernetes node: %w", err))
	}
	if err := m.topolvm.DeleteNodeResources(ctx, controlNodeName, nodeName); err != nil {
		errs = append(errs, fmt.Errorf("delete TopoLVM node resources: %w", err))
	}
	return errors.Join(errs...)
}

func (m *manager) removeWorkerContainer(ctx context.Context, nodeName string) error {
	var errs []error
	if err := m.node.Stop(ctx, nodeName); err != nil {
		errs = append(errs, fmt.Errorf("stop node container: %w", err))
	}
	if err := m.node.Remove(ctx, nodeName); err != nil {
		errs = append(errs, fmt.Errorf("remove node container: %w", err))
	}
	return errors.Join(errs...)
}

func (m *manager) removeWorkerStorage(ctx context.Context, nodeName string) error {
	var errs []error
	if err := m.topolvm.DeleteBackends(ctx, nodeName); err != nil {
		errs = append(errs, fmt.Errorf("delete node storage: %w", err))
	}
	if err := m.node.RemoveState(m.cfg.StateDir, nodeName); err != nil {
		errs = append(errs, fmt.Errorf("remove node state: %w", err))
	}
	return errors.Join(errs...)
}

func removeWorker(nodesCfg rootconfig.NodesConfig, nodeName string) rootconfig.NodesConfig {
	for i, node := range nodesCfg.Nodes {
		if node.NodeName == nodeName {
			nodesCfg.Nodes = slices.Delete(nodesCfg.Nodes, i, i+1)
			break
		}
	}
	return nodesCfg
}

func (m *manager) persistNodeRemoval(nodesCfg rootconfig.NodesConfig) error {
	if err := writeNodesConfig(m.clusterName, nodesCfg); err != nil {
		return fmt.Errorf("write nodes config: %w", err)
	}
	return nil
}

func nodeIndex(name string) (int, bool) {
	index := strings.LastIndexByte(name, '-')
	if index < 0 {
		return 0, false
	}
	number, err := strconv.Atoi(name[index+1:])
	return number, err == nil && number >= 1
}
