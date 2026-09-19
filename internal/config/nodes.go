package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const nodesConfigFileName = "nodes.json"

func NodesConfigPath(clusterName string) string {
	return filepath.Join(ConfigDir(), clusterName, nodesConfigFileName)
}

func ReadNodesConfig(clusterName string) (NodesConfig, error) {
	data, err := os.ReadFile(NodesConfigPath(clusterName))
	if errors.Is(err, os.ErrNotExist) {
		return NodesConfig{}, nil
	}
	if err != nil {
		return NodesConfig{}, err
	}
	var cfg NodesConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return NodesConfig{}, err
	}
	return cfg, nil
}

func WriteNodesConfig(clusterName string, cfg NodesConfig) error {
	path := NodesConfigPath(clusterName)
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
