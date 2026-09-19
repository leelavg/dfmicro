package node

import (
	"encoding/json"
	"os"
	"path/filepath"

	rootconfig "dfmicro/internal/config"
)

type NodeConfig = rootconfig.NodeRecord
type NodesConfig = rootconfig.NodesConfig

type nodeOutput struct {
	Role string `json:"role"`
	rootconfig.NodeConfig
	ControlNodeName string `json:"controlNodeName,omitempty"`
	Kubeconfig      string `json:"kubeconfig,omitempty"`
}

func nodesConfigPath(clusterName string) string {
	return rootconfig.NodesConfigPath(clusterName)
}

func ReadNodesConfig(clusterName string) (NodesConfig, error) {
	return rootconfig.ReadNodesConfig(clusterName)
}

func WriteNodesConfig(clusterName string, cfg NodesConfig) error {
	return rootconfig.WriteNodesConfig(clusterName, cfg)
}

func printNodesConfig(clusterName string) error {
	nodesCfg, err := ReadNodesConfig(clusterName)
	if err != nil {
		return err
	}

	nodes := make([]nodeOutput, 0, len(nodesCfg.Nodes))
	for _, node := range nodesCfg.Nodes {
		role := "worker"
		if node.Index == 0 {
			role = "control"
		}
		nodes = append(nodes, nodeOutput{
			Role:            role,
			NodeConfig:      node.NodeConfig,
			ControlNodeName: node.ControlNodeName,
			Kubeconfig:      node.Kubeconfig,
		})
	}
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
	controlNodeName := rootconfig.NodeName(clusterName, 0)
	return filepath.Join(rootconfig.ConfigDir(), clusterName, controlNodeName, "bootstrap-kubeconfig")
}
