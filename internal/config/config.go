package config

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"dfmicro/internal/support"
)

//go:embed defaults.json
var embeddedConfig []byte

type Config struct {
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
	UseEtcd             bool    `json:"useEtcd"`

	BridgeName   string `json:"bridgeName"`
	BridgeSubnet string `json:"bridgeSubnet"`

	GroupCount       int    `json:"groupCount"`
	ClustersPerGroup int    `json:"clustersPerGroup"`
	ReservePerGroup  int    `json:"reservePerGroup"`
	NADNamespace     string `json:"nadNamespace"`
}

type NetworkCIDRs struct {
	Cluster string
	Service string
}

var Load = sync.OnceValue(func() Config {
	var cfg Config
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
