package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

const configFileName = "config.json"

type Config struct {
	rootconfig.Config
	Name                  string `json:"name,omitempty"`
	Network               string `json:"network,omitempty"`
	StateDir              string `json:"stateDir,omitempty"`
	LVMDisk               string `json:"lvmDisk,omitempty"`
	ExtraConfig           string `json:"extraConfig,omitempty"`
	DefaultKubeconfigPath string `json:"defaultKubeconfig,omitempty"`
	VGName                string `json:"vgName,omitempty"`
}

func NewConfigFromCommand(cmd *cli.Command) (Config, error) {
	defaults := rootconfig.Load()

	name := cmd.String("name")
	cfg := deriveConfig(defaults, name)

	if cmd.IsSet("image") {
		cfg.Image = cmd.String("image")
	}
	if cmd.IsSet("lvm-volsize") {
		cfg.LVMVolSize = cmd.String("lvm-volsize")
	}
	if cmd.IsSet("api-server-port") {
		cfg.APIServerPort = cmd.Int("api-server-port")
	}
	if cmd.IsSet("no-expose-kubeapi") {
		cfg.ExposeKubeAPI = !cmd.Bool("no-expose-kubeapi")
	}
	if cmd.IsSet("overprovision-ratio") {
		cfg.OverprovisionRatio = cmd.Float32("overprovision-ratio")
	}

	return cfg, nil
}

func deriveConfig(defaults rootconfig.Config, name string) Config {
	stateDir := filepath.Join(configDir(), name)

	return Config{
		Config:                defaults,
		Name:                  name,
		Network:               name,
		StateDir:              stateDir,
		LVMDisk:               filepath.Join(stateDir, name+".image"),
		ExtraConfig:           filepath.Join(stateDir, "custom_config.yaml"),
		DefaultKubeconfigPath: filepath.Join(stateDir, "kubeconfig"),
		VGName:                name,
	}
}

func ClusterConfigPath(name string) (string, error) {
	return filepath.Join(configDir(), name, configFileName), nil
}

func configDir() string {
	dir := support.Must(os.UserConfigDir())
	return filepath.Join(dir, "dfmicro")
}

func ReadClusterConfig(name string) (Config, error) {
	path, err := ClusterConfigPath(name)
	if err != nil {
		return Config{}, err
	}
	return readConfigFile(path)
}

func WriteClusterConfig(cfg Config) error {
	path, err := ClusterConfigPath(cfg.Name)
	if err != nil {
		return err
	}

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

func readConfigFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
