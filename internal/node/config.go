package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"dfmicro/internal/cluster"
	rootconfig "dfmicro/internal/config"
)

const nodesConfigFileName = "nodes.json"

type NodeConfig = rootconfig.WorkerConfig

type NodesConfig struct {
	Nodes []NodeConfig `json:"nodes"`
}

type nodeOutput struct {
	Role string `json:"role"`
	rootconfig.NodeConfig
	ControlNodeName string `json:"controlNodeName,omitempty"`
	Kubeconfig      string `json:"kubeconfig,omitempty"`
}

func nodesConfigPath(clusterName string) string {
	return filepath.Join(rootconfig.ConfigDir(), clusterName, nodesConfigFileName)
}

func ReadNodesConfig(clusterName string) (NodesConfig, error) {
	path := nodesConfigPath(clusterName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NodesConfig{}, nil
		}
		return NodesConfig{}, err
	}
	var cfg NodesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return NodesConfig{}, err
	}
	return cfg, nil
}

func WriteNodesConfig(clusterName string, cfg NodesConfig) error {
	path := nodesConfigPath(clusterName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func printNodesConfig(clusterName string) error {
	clusterCfg, err := cluster.ReadClusterConfig(clusterName)
	if err != nil {
		return err
	}
	nodesCfg, err := ReadNodesConfig(clusterName)
	if err != nil {
		return err
	}

	nodes := []nodeOutput{{
		Role:       "control",
		NodeConfig: clusterCfg.NodeConfig,
		Kubeconfig: clusterCfg.Kubeconfig,
	}}
	for _, node := range nodesCfg.Nodes {
		nodes = append(nodes, nodeOutput{
			Role:            "worker",
			NodeConfig:      node.NodeConfig,
			ControlNodeName: node.ControlNodeName,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeName < nodes[j].NodeName })
	output := struct {
		Nodes []nodeOutput `json:"nodes"`
	}{Nodes: nodes}
	data, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(data, '\n'))
	return err
}

func bootstrapKubeconfigPath(clusterName string) string {
	controlNodeName := clusterName + "-1"
	return filepath.Join(rootconfig.ConfigDir(), clusterName, controlNodeName, "bootstrap-kubeconfig")
}
