package locals3

import (
	_ "embed"
	"encoding/json"
	"log/slog"

	"dfmicro/internal/execx"
)

const (
	containerName = "dfmicro-local-s3"
	volumeName    = "dfmicro-local-s3-data"
)

//go:embed defaults.json
var embeddedDefaults []byte

type manager struct {
	logger *slog.Logger
	runner execx.Runner
}

func defaultImage() string {
	var cfg struct {
		Image string `json:"image"`
	}
	if err := json.Unmarshal(embeddedDefaults, &cfg); err != nil {
		panic(err)
	}
	return cfg.Image
}
