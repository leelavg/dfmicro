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

	"dfmicro/internal/cluster"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/network"
	"dfmicro/internal/support"
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

	if len(nodesCfg.Nodes) == 0 || nodesCfg.Nodes[0].Index != 0 {
		return fmt.Errorf("cluster %q has no control node in nodes.json", m.clusterName)
	}
	controlNodeName := nodesCfg.Nodes[0].NodeName
	if !force {
		if err := m.waitForControlPlane(ctx, controlNodeName); err != nil {
			return err
		}
	}
	if err := network.ValidateWorkerAdd(m.clusterName); err != nil {
		return err
	}
	nodeIndex := support.FirstAvailableIndex(nodesCfg.Nodes, func(node NodeConfig) int {
		return node.Index
	})
	nodeName := rootconfig.NodeName(m.clusterName, nodeIndex)
	nodeStateDir := filepath.Join(cfg.StateDir, nodeName)
	if err := os.MkdirAll(nodeStateDir, 0o755); err != nil {
		return fmt.Errorf("create node state directory: %w", err)
	}

	if cfg.EnableTopoLVM {
		topolvm := support.NewTopoLVM(m.runner, cfg.StateDir, support.TopoLVMConfig{
			VolumeSize:         cfg.LVMVolSize,
			OverprovisionRatio: cfg.OverprovisionRatio,
			Thinpool:           cfg.EnableThinpool,
		})
		if err := topolvm.CreateBackend(ctx, nodeName); err != nil {
			return fmt.Errorf("create node storage: %w", err)
		}
	}

	if err := m.extractBootstrapKubeconfig(ctx, cfg, controlNodeName); err != nil {
		return fmt.Errorf("extract bootstrap kubeconfig: %w", err)
	}

	if err := m.extractKubeletCA(ctx, cfg, controlNodeName, nodeName); err != nil {
		return fmt.Errorf("extract kubelet CA: %w", err)
	}

	controlNodeIP, err := support.GetContainerIP(ctx, m.runner, cfg.BridgeName, controlNodeName)
	if err != nil {
		return fmt.Errorf("get control node IP: %w", err)
	}

	if err := m.addWorkerNode(ctx, cfg, nodeName, controlNodeName, controlNodeIP, mounts); err != nil {
		return fmt.Errorf("add worker node: %w", err)
	}
	if cfg.EnableTopoLVM {
		topolvm := support.NewTopoLVM(m.runner, cfg.StateDir, support.TopoLVMConfig{
			VolumeSize:         cfg.LVMVolSize,
			OverprovisionRatio: cfg.OverprovisionRatio,
			Thinpool:           cfg.EnableThinpool,
		})
		if err := topolvm.Reconcile(ctx, m.clusterName, controlNodeName); err != nil {
			return fmt.Errorf("apply topolvm manifest: %w", err)
		}
	}
	if multiNode, err := network.UsesWhereabouts(m.clusterName); err != nil {
		return fmt.Errorf("read multi-node state: %w", err)
	} else if multiNode {
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "wait", "--for=create", "--timeout=120s", "node/"+nodeName); err != nil {
			return fmt.Errorf("wait for worker node before labeling: %w", err)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "label", "node", nodeName, "dfmicro.io/whereabouts=enabled", "--overwrite"); err != nil {
			return fmt.Errorf("label worker node for whereabouts: %w", err)
		}
	}

	nodesCfg.Nodes = slices.Insert(nodesCfg.Nodes, nodeIndex, NodeConfig{
		NodeConfig: rootconfig.NodeConfig{
			Index:      nodeIndex,
			NodeName:   nodeName,
			LVMDisk:    filepath.Join(nodeStateDir, nodeName+".image"),
			VGName:     nodeName,
			PullSecret: cfg.PullSecret,
			IDMSFiles:  append([]string(nil), cfg.IDMSFiles...),
			Mounts:     append([]string(nil), mounts...),
		},
		ControlNodeName: controlNodeName,
	})

	if err := WriteNodesConfig(m.clusterName, nodesCfg); err != nil {
		return fmt.Errorf("write nodes config: %w", err)
	}

	m.logger.Info("worker node added", "cluster", m.clusterName, "node", nodeName, "control", controlNodeName)
	return nil
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
	workerCertsDir := filepath.Join(cfg.StateDir, nodeName, "certs")
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

func (m *manager) addWorkerNode(ctx context.Context, cfg cluster.Config, nodeName, controlNodeName, controlNodeIP string, mounts []string) error {
	nodeStateDir := filepath.Join(cfg.StateDir, nodeName)
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

	multinodePath := filepath.Join(nodeStateDir, "20-multinode.yaml")
	if err := os.WriteFile(multinodePath, multinodeBuf.Bytes(), 0o644); err != nil {
		return err
	}

	bootstrapSource := bootstrapKubeconfigPath(cfg.Name)
	bootstrapTarget := "/var/lib/microshift/bootstrap/kubeconfig"

	// Mount control plane's kubelet CA (not serving cert) so worker generates own cert signed by same CA
	csrSignerDir := filepath.Join(nodeStateDir, "certs", "kubelet-csr-signer-signer", "csr-signer")
	csrSignerTarget := "/var/lib/microshift/certs/kubelet-csr-signer-signer/csr-signer"
	caBundleFile := filepath.Join(nodeStateDir, "certs", "kubelet-ca.crt")
	caBundleTarget := "/var/lib/microshift/certs/ca-bundle/kubelet-ca.crt"
	extraArgs := []string{
		"--add-host", controlNodeName + ":" + controlNodeIP,
	}

	networkData := struct {
		Clients     []string
		ClusterCIDR string
		ServiceCIDR string
		BaseDomain  string
	}{
		Clients:     nil,
		ClusterCIDR: cfg.ClusterCIDR,
		ServiceCIDR: cfg.ServiceCIDR,
		BaseDomain:  cfg.Name + ".dfmicro.io",
	}

	var networkBuf bytes.Buffer
	networkTmpl := template.Must(template.New("").Parse(networkConfigTmpl))
	if err := networkTmpl.Execute(&networkBuf, networkData); err != nil {
		return err
	}

	networkPath := filepath.Join(nodeStateDir, "15-networking.yaml")
	if err := os.WriteFile(networkPath, networkBuf.Bytes(), 0o644); err != nil {
		return err
	}
	extraArgs = append(extraArgs,
		"--volume", networkPath+":/etc/microshift/config.d/15-networking.yaml:ro",
		"--volume", multinodePath+":/etc/microshift/config.d/20-multinode.yaml:ro",
		"--volume", bootstrapSource+":"+bootstrapTarget+":ro",
		"--volume", bootstrapSource+":/var/lib/microshift/resources/kubeadmin/kubeconfig",
		"--volume", csrSignerDir+":"+csrSignerTarget,
		"--volume", caBundleFile+":"+caBundleTarget+":ro",
	)

	crioDropinPath := filepath.Join(nodeStateDir, "20-multus-cni-plugins.conf")
	if err := os.WriteFile(crioDropinPath, []byte(multusDropinConfig), 0o644); err != nil {
		return err
	}
	extraArgs = append(extraArgs, "--volume", crioDropinPath+":/etc/crio/crio.conf.d/20-multus-cni-plugins.conf:ro")

	if err := support.NewNode(m.logger, m.runner).Create(ctx, support.NodeSpec{
		Name:                nodeName,
		Image:               cfg.Image,
		Network:             cfg.BridgeName,
		StateDir:            nodeStateDir,
		ClusterName:         cfg.Name,
		ShareHostContainers: cfg.ShareHostContainers,
		PullSecret:          cfg.PullSecret,
		IDMSFiles:           cfg.IDMSFiles,
		Mounts:              mounts,
		ExtraArgs:           extraArgs,
	}); err != nil {
		return fmt.Errorf("create worker node: %w", err)
	}
	if err := support.TrustContainerNetwork(ctx, m.runner, nodeName,
		[]string{cfg.ClusterCIDR, cfg.ServiceCIDR, cfg.BridgeSubnet},
		[]string{"eth0"},
	); err != nil {
		return fmt.Errorf("trust cluster CIDRs: %w", err)
	}
	apiIP, err := apiServerIP(cfg.ServiceCIDR)
	if err != nil {
		return fmt.Errorf("calculate API server IP: %w", err)
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", nodeName, "ip", "route", "replace", apiIP+"/32", "via", controlNodeIP, "dev", "eth0"); err != nil {
		return fmt.Errorf("route API server IP through control node: %w", err)
	}
	m.logger.Info("waiting for worker readiness", "node", nodeName)
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "wait", "--for=create", "--timeout=120s", "node/"+nodeName); err != nil {
		return fmt.Errorf("wait for worker registration: %w", err)
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "wait", "--for=condition=Ready", "--timeout=120s", "node/"+nodeName); err != nil {
		return fmt.Errorf("wait for worker readiness: %w", err)
	}
	m.logger.Info("waiting for worker CNI/network", "node", nodeName)
	if err := support.WaitForCNI(ctx, m.runner, nodeName); err != nil {
		return fmt.Errorf("wait for worker CNI/network: %w", err)
	}
	m.logger.Info("worker CNI/network is ready", "node", nodeName)
	m.logger.Info("worker is ready", "node", nodeName)
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
	if !strings.HasPrefix(nodeName, m.clusterName+"-") {
		return fmt.Errorf("node %q does not belong to cluster %q", nodeName, m.clusterName)
	}
	if _, ok := nodeIndex(nodeName); !ok {
		return fmt.Errorf("node %q is not a worker node", nodeName)
	}

	var cleanupErrs []error
	if len(nodesCfg.Nodes) == 0 || nodesCfg.Nodes[0].Index != 0 {
		return fmt.Errorf("cluster %q has no control node in nodes.json", m.clusterName)
	}
	controlNodeName := nodesCfg.Nodes[0].NodeName
	node := support.NewNode(m.logger, m.runner)
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "delete", "node", nodeName, "--ignore-not-found", "--wait=false"); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("delete Kubernetes node: %w", err))
	}
	if cfg.EnableTopoLVM {
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "exec", controlNodeName, "kubectl", "-n", "topolvm-system", "delete", "daemonset,configmap", "-l", "dfmicro.io/topolvm-node="+nodeName, "--ignore-not-found"); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete TopoLVM node resources: %w", err))
		}
	}
	if err := node.Stop(ctx, nodeName); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("stop node container: %w", err))
	}

	if err := node.Remove(ctx, nodeName); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove node container: %w", err))
	}

	for i, node := range nodesCfg.Nodes {
		if node.NodeName == nodeName {
			nodesCfg.Nodes = slices.Delete(nodesCfg.Nodes, i, i+1)
			break
		}
	}
	if cfg.EnableTopoLVM {
		topolvm := support.NewTopoLVM(m.runner, cfg.StateDir, support.TopoLVMConfig{
			VolumeSize:         cfg.LVMVolSize,
			OverprovisionRatio: cfg.OverprovisionRatio,
			Thinpool:           cfg.EnableThinpool,
		})
		if err := topolvm.DeleteBackends(ctx, nodeName); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("delete node storage: %w", err))
		}
	}
	if err := node.RemoveState(cfg.StateDir, nodeName); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove node state: %w", err))
	}
	if cfg.EnableTopoLVM {
		topolvm := support.NewTopoLVM(m.runner, cfg.StateDir, support.TopoLVMConfig{
			VolumeSize:         cfg.LVMVolSize,
			OverprovisionRatio: cfg.OverprovisionRatio,
			Thinpool:           cfg.EnableThinpool,
		})
		if err := topolvm.Reconcile(ctx, m.clusterName, controlNodeName); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("apply TopoLVM manifest: %w", err))
		}
	}

	if err := WriteNodesConfig(m.clusterName, nodesCfg); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("write nodes config: %w", err))
	}

	m.logger.Info("worker node deleted", "cluster", m.clusterName, "node", nodeName)
	return errors.Join(cleanupErrs...)
}

func nodeIndex(name string) (int, bool) {
	index := strings.LastIndexByte(name, '-')
	if index < 0 {
		return 0, false
	}
	number, err := strconv.Atoi(name[index+1:])
	return number, err == nil && number >= 1
}
