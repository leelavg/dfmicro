package config

import (
	_ "embed"
	"encoding/json"

	"dfmicro/internal/support"
)

//go:embed defaults.json
var embeddedConfig []byte

type Config struct {
	Image              string  `json:"image"`
	LVMVolSize         string  `json:"lvmVolSize"`
	APIServerPort      int     `json:"apiServerPort"`
	ExposeKubeAPI      bool    `json:"exposeKubeAPI"`
	OverprovisionRatio float32 `json:"overprovisionRatio"`
}

func Load() Config {
	var cfg Config
	support.MustOK(json.Unmarshal(embeddedConfig, &cfg))
	return cfg
}
