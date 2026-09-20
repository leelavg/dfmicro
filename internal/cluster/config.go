package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	rootconfig "dfmicro/internal/config"

	"github.com/urfave/cli/v3"
)

func newConfigFromCommand(cmd *cli.Command) (rootconfig.ClusterConfig, error) {
	name := cmd.String("name")
	cfg := deriveConfig(defaultRootConfig.ClusterDefaults, name)

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
			return rootconfig.ClusterConfig{}, fmt.Errorf("pull-secret: %w", err)
		}
		cfg.PullSecret = abs
	}
	for _, f := range cmd.StringSlice("idms") {
		abs, err := filepath.Abs(f)
		if err != nil {
			return rootconfig.ClusterConfig{}, fmt.Errorf("idms %s: %w", f, err)
		}
		cfg.IDMSFiles = append(cfg.IDMSFiles, abs)
	}
	cfg.Mounts = cmd.StringSlice("mount")

	if cmd.IsSet("no-expose-kubeapi") {
		cfg.ExposeKubeAPI = !cmd.Bool("no-expose-kubeapi")
	}
	if cmd.IsSet("no-share-host-containers") {
		cfg.ShareHostContainers = !cmd.Bool("no-share-host-containers")
	}
	if cmd.IsSet("no-power-tuning") {
		cfg.PowerTuning = !cmd.Bool("no-power-tuning")
	}
	if cmd.IsSet("no-topolvm") {
		cfg.EnableTopoLVM = !cmd.Bool("no-topolvm")
	}
	if cmd.IsSet("no-thinpool") {
		cfg.EnableThinpool = !cmd.Bool("no-thinpool")
	}
	if cmd.IsSet("etcd") {
		cfg.UseEtcd = cmd.Bool("etcd")
	}

	return cfg, nil
}

func deriveConfig(defaults rootconfig.ClusterDefaults, name string) rootconfig.ClusterConfig {
	stateDir := filepath.Join(rootconfig.ConfigDir(), name)
	controlNodeName := rootconfig.NodeName(name, 0)
	controlNodeDir := filepath.Join(stateDir, controlNodeName)

	cfg := rootconfig.ClusterConfig{
		ClusterDefaults: defaults,
		StateDir:        stateDir,
		ControlConfig: rootconfig.ControlConfig{
			NodeConfig: rootconfig.NodeConfig{
				Index:    0,
				NodeName: controlNodeName,
				LVMDisk:  filepath.Join(controlNodeDir, controlNodeName+".image"),
				VGName:   controlNodeName,
			},
			Kubeconfig: filepath.Join(controlNodeDir, "kubeconfig"),
		},
	}
	cfg.Name = name
	return cfg
}

func printClusterConfig(name string) error {
	cfg, err := rootconfig.ReadClusterConfig(name)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg.ClusterDefaults, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	_, err = os.Stdout.Write(data)
	return err
}
