package node

import (
	"encoding/json"
	"os"
	"path/filepath"

	rootconfig "dfmicro/internal/config"
)

const nodesConfigFileName = "nodes.json"

type NodeConfig struct {
	Name            string `json:"name"`
	ControlNodeName string `json:"controlNodeName"`
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

func bootstrapKubeconfigPath(clusterName string) string {
	return filepath.Join(rootconfig.ConfigDir(), clusterName, "bootstrap-kubeconfig")
}
