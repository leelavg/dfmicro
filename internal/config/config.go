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
	Name                string  `json:"name"`
	Image               string  `json:"image"`
	LVMVolSize          string  `json:"lvmVolSize"`
	APIServerPort       int     `json:"apiServerPort"`
	NodeCIDR            string  `json:"nodeCIDR"`
	ClusterCIDR         string  `json:"clusterCIDR"`
	ServiceCIDR         string  `json:"serviceCIDR"`
	BridgeSubnet        string  `json:"bridgeSubnet"`
	ExposeKubeAPI       bool    `json:"exposeKubeAPI"`
	OverprovisionRatio  float32 `json:"overprovisionRatio"`
	ShareHostContainers bool    `json:"shareHostContainers"`
	PowerTuning         bool    `json:"powerTuning"`
	EnableThinpool      bool    `json:"enableThinpool"`
	NADNamespace        string  `json:"nadNamespace"`
	BridgeSegmentCount  int     `json:"bridgeSegmentCount"`
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
