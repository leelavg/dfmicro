package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const clusterConfigFileName = "config.json"

func ClusterConfigPath(clusterName string) string {
	return filepath.Join(ConfigDir(), clusterName, clusterConfigFileName)
}

func ReadClusterConfig(clusterName string) (ClusterConfig, error) {
	data, err := os.ReadFile(ClusterConfigPath(clusterName))
	if err != nil {
		return ClusterConfig{}, err
	}
	var cfg ClusterConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ClusterConfig{}, err
	}
	return cfg, nil
}

func WriteClusterConfig(cfg ClusterConfig) error {
	path := ClusterConfigPath(cfg.Name)
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

func Kubeconfig(clusterName string) (string, error) {
	cfg, err := ReadClusterConfig(clusterName)
	if err != nil {
		return "", err
	}
	return cfg.Kubeconfig, nil
}

func GetCIDRs(clusterName string) (NetworkCIDRs, error) {
	cfg, err := ReadClusterConfig(clusterName)
	if err != nil {
		return NetworkCIDRs{}, err
	}
	return NetworkCIDRs{Cluster: cfg.ClusterCIDR, Service: cfg.ServiceCIDR}, nil
}
