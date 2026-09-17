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

type NodeConfig struct {
	Name            string   `json:"name"`
	ControlNodeName string   `json:"controlNodeName"`
	Mounts          []string `json:"mounts,omitempty"`
}

type NodesConfig struct {
	Nodes []NodeConfig `json:"nodes"`
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

	type nodeOutput struct {
		Name            string   `json:"name"`
		Role            string   `json:"role"`
		ControlNodeName string   `json:"controlNodeName,omitempty"`
		StateDir        string   `json:"stateDir"`
		LVMDisk         string   `json:"lvmDisk,omitempty"`
		VGName          string   `json:"vgName,omitempty"`
		PullSecret      string   `json:"pullSecret,omitempty"`
		IDMSFiles       []string `json:"idmsFiles,omitempty"`
		Mounts          []string `json:"mounts,omitempty"`
	}
	controlNodeName := clusterCfg.Name + "-1"
	nodes := []nodeOutput{{
		Name:       controlNodeName,
		Role:       "control",
		StateDir:   filepath.Join(clusterCfg.StateDir, controlNodeName),
		LVMDisk:    clusterCfg.LVMDisk,
		VGName:     clusterCfg.VGName,
		PullSecret: clusterCfg.PullSecret,
		IDMSFiles:  clusterCfg.IDMSFiles,
		Mounts:     clusterCfg.ExtraMounts,
	}}
	for _, node := range nodesCfg.Nodes {
		nodes = append(nodes, nodeOutput{
			Name:            node.Name,
			Role:            "worker",
			ControlNodeName: node.ControlNodeName,
			StateDir:        filepath.Join(clusterCfg.StateDir, node.Name),
			LVMDisk:         filepath.Join(clusterCfg.StateDir, node.Name, node.Name+".image"),
			VGName:          node.Name,
			PullSecret:      clusterCfg.PullSecret,
			IDMSFiles:       clusterCfg.IDMSFiles,
			Mounts:          node.Mounts,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
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
