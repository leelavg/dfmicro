package config

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"dfmicro/internal/support"
)

func NodeName(cluster string, index int) string {
	return fmt.Sprintf("%s-%d", cluster, index)
}

//go:embed defaults.json
var embeddedConfig []byte

type ClusterDefaults struct {
	Name        string `json:"name"`
	Image       string `json:"image"`
	PowerTuning bool   `json:"powerTuning"`

	APIServerPort int    `json:"apiServerPort"`
	ClusterCIDR   string `json:"clusterCIDR"`
	ServiceCIDR   string `json:"serviceCIDR"`
	ExposeKubeAPI bool   `json:"exposeKubeAPI"`

	LVMVolSize          string  `json:"lvmVolSize"`
	OverprovisionRatio  float32 `json:"overprovisionRatio"`
	ShareHostContainers bool    `json:"shareHostContainers"`
	EnableThinpool      bool    `json:"enableThinpool"`
	EnableTopoLVM       bool    `json:"enableTopoLVM"`
	UseEtcd             bool    `json:"useEtcd"`

	BridgeName   string `json:"bridgeName"`
	BridgeSubnet string `json:"bridgeSubnet"`
}

type NetworkDefaults struct {
	GroupCount       int    `json:"groupCount"`
	ClustersPerGroup int    `json:"clustersPerGroup"`
	ReservePerGroup  int    `json:"reservePerGroup"`
	NADNamespace     string `json:"nadNamespace"`
}

type NodeConfig struct {
	Index      int      `json:"index"`
	NodeName   string   `json:"nodeName"`
	LVMDisk    string   `json:"lvmDisk,omitempty"`
	VGName     string   `json:"vgName,omitempty"`
	PullSecret string   `json:"pullSecret,omitempty"`
	IDMSFiles  []string `json:"idmsFiles,omitempty"`
	Mounts     []string `json:"mounts,omitempty"`
}

type ControlConfig struct {
	NodeConfig
	Kubeconfig string `json:"kubeconfig,omitempty"`
}

type ClusterConfig struct {
	ClusterDefaults
	ControlConfig
	StateDir string `json:"stateDir,omitempty"`
}

type NodesConfig struct {
	Nodes []NodeConfig `json:"nodes"`
}

type Defaults struct {
	ClusterDefaults
	NetworkDefaults
}

type NetworkCIDRs struct {
	Cluster string
	Service string
}

var Load = sync.OnceValue(func() Defaults {
	var cfg Defaults
	support.MustOK(json.Unmarshal(embeddedConfig, &cfg))
	return cfg
})

func ConfigDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = support.Must(os.UserHomeDir())
	}
	return filepath.Join(configDir, "dfmicro")
}
