package node

import (
	"encoding/json"
	"os"
	"path/filepath"

	rootconfig "dfmicro/internal/config"
)

type nodesConfig = rootconfig.NodesConfig

type nodeOutput struct {
	Role string `json:"role"`
	rootconfig.NodeConfig
	ControlNodeName string `json:"controlNodeName,omitempty"`
	Kubeconfig      string `json:"kubeconfig,omitempty"`
}

func readNodesConfig(clusterName string) (nodesConfig, error) {
	return rootconfig.ReadNodesConfig(clusterName)
}

func writeNodesConfig(clusterName string, cfg nodesConfig) error {
	return rootconfig.WriteNodesConfig(clusterName, cfg)
}

func printNodesConfig(clusterName string) error {
	clusterCfg, err := rootconfig.ReadClusterConfig(clusterName)
	if err != nil {
		return err
	}
	nodesCfg, err := readNodesConfig(clusterName)
	if err != nil {
		return err
	}

	nodes := make([]nodeOutput, 0, len(nodesCfg.Nodes))
	for _, node := range nodesCfg.Nodes {
		role := "worker"
		output := nodeOutput{Role: role, NodeConfig: node}
		if node.Index == 0 {
			role = "control"
			output.Role = role
			output.Kubeconfig = clusterCfg.Kubeconfig
		} else {
			output.ControlNodeName = clusterCfg.NodeName
		}
		nodes = append(nodes, output)
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
