package cluster

import (
	cli "github.com/urfave/cli/v3"
)

type Config struct {
	ClusterName   string
	NodeBaseName  string
	Image         string
	LVMDisk       string
	ExtraConfig   string
	LVMVolSize    string
	APIServerPort int
	VGName        string
	ExposeKubeAPI bool
}

func NewConfigFromCommand(cmd *cli.Command) (Config, error) {
	cfg := Config{
		ClusterName:   cmd.String("cluster-name"),
		NodeBaseName:  cmd.String("node-base-name"),
		Image:         cmd.String("image"),
		LVMDisk:       cmd.String("lvm-disk"),
		ExtraConfig:   cmd.String("extra-config"),
		LVMVolSize:    cmd.String("lvm-volsize"),
		APIServerPort: cmd.Int("api-server-port"),
		VGName:        cmd.String("vg-name"),
		ExposeKubeAPI: cmd.Bool("expose-kubeapi"),
	}

	return cfg, nil
}
