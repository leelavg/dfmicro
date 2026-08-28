package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	rootconfig "dfmicro/internal/config"

	"github.com/urfave/cli/v3"
)

const configFileName = "config.json"

type config struct {
	rootconfig.Config
	Name                  string   `json:"name,omitempty"`
	StateDir              string   `json:"stateDir,omitempty"`
	LVMDisk               string   `json:"lvmDisk,omitempty"`
	ExtraConfig           string   `json:"extraConfig,omitempty"`
	DefaultKubeconfigPath string   `json:"defaultKubeconfig,omitempty"`
	VGName                string   `json:"vgName,omitempty"`
	PullSecret            string   `json:"pullSecret,omitempty"`
	IDMSFiles             []string `json:"idmsFiles,omitempty"`
	ExtraMounts           []string `json:"extraMounts,omitempty"`
}

func newConfigFromCommand(cmd *cli.Command) (config, error) {
	name := cmd.String("name")
	cfg := deriveConfig(defaultRootConfig, name)

	cfg.Image = cmd.String("image")
	cfg.LVMVolSize = cmd.String("lvm-volsize")
	cfg.APIServerPort = cmd.Int("api-server-port")
	cfg.BridgeSubnet = cmd.String("bridge-subnet")
	cfg.ClusterCIDR = cmd.String("cluster-cidr")
	cfg.ServiceCIDR = cmd.String("service-cidr")
	cfg.OverprovisionRatio = cmd.Float32("overprovision-ratio")
	if s := cmd.String("pull-secret"); s != "" {
		abs, err := filepath.Abs(s)
		if err != nil {
			return config{}, fmt.Errorf("pull-secret: %w", err)
		}
		cfg.PullSecret = abs
	}
	for _, f := range cmd.StringSlice("idms") {
		abs, err := filepath.Abs(f)
		if err != nil {
			return config{}, fmt.Errorf("idms %s: %w", f, err)
		}
		cfg.IDMSFiles = append(cfg.IDMSFiles, abs)
	}
	cfg.ExtraMounts = cmd.StringSlice("mount")

	if cmd.IsSet("no-expose-kubeapi") {
		cfg.ExposeKubeAPI = !cmd.Bool("no-expose-kubeapi")
	}
	if cmd.IsSet("no-share-host-containers") {
		cfg.ShareHostContainers = !cmd.Bool("no-share-host-containers")
	}
	if cmd.IsSet("no-power-tuning") {
		cfg.PowerTuning = !cmd.Bool("no-power-tuning")
	}
	if cmd.IsSet("no-thinpool") {
		cfg.EnableThinpool = !cmd.Bool("no-thinpool")
	}
	if cmd.IsSet("etcd") {
		cfg.UseEtcd = cmd.Bool("etcd")
	}

	return cfg, nil
}

func deriveConfig(defaults rootconfig.Config, name string) config {
	stateDir := filepath.Join(rootconfig.ConfigDir(), name)

	return config{
		Config:                defaults,
		Name:                  name,
		StateDir:              stateDir,
		LVMDisk:               filepath.Join(stateDir, name+".image"),
		ExtraConfig:           filepath.Join(stateDir, "custom_config.yaml"),
		DefaultKubeconfigPath: filepath.Join(stateDir, "kubeconfig"),
		VGName:                name,
	}
}

func clusterConfigPath(name string) (string, error) {
	return filepath.Join(rootconfig.ConfigDir(), name, configFileName), nil
}

func Kubeconfig(name string) (string, error) {
	cfg, err := readClusterConfig(name)
	if err != nil {
		return "", err
	}
	return cfg.DefaultKubeconfigPath, nil
}

func GetCIDRs(name string) (rootconfig.NetworkCIDRs, error) {
	cfg, err := readClusterConfig(name)
	if err != nil {
		return rootconfig.NetworkCIDRs{}, err
	}
	return rootconfig.NetworkCIDRs{
		Cluster: cfg.ClusterCIDR,
		Service: cfg.ServiceCIDR,
	}, nil
}

func readClusterConfig(name string) (config, error) {
	path, err := clusterConfigPath(name)
	if err != nil {
		return config{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return config{}, err
	}

	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func writeClusterConfig(cfg config) error {
	path, err := clusterConfigPath(cfg.Name)
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

func printClusterConfig(name string) error {
	cfg, err := readClusterConfig(name)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	_, err = os.Stdout.Write(data)
	return err
}
