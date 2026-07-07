package config

import (
	_ "embed"
	"encoding/json"
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
	ExposeKubeAPI       bool    `json:"exposeKubeAPI"`
	OverprovisionRatio  float32 `json:"overprovisionRatio"`
	ShareHostContainers bool    `json:"shareHostContainers"`
}

var Load = sync.OnceValue(func() Config {
	var cfg Config
	support.MustOK(json.Unmarshal(embeddedConfig, &cfg))
	return cfg
})
