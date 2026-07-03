package app

import (
	"context"
	"fmt"
	"log/slog"

	"dfmicro/internal/cluster"
	"dfmicro/internal/execx"

	cli "github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	flags := []cli.Flag{
		&cli.StringFlag{
			Name:  "cluster-name",
			Usage: "Podman network name for the cluster",
			Value: "microshift-okd-multinode",
		},
		&cli.StringFlag{
			Name:  "node-base-name",
			Usage: "Base name prefix for cluster node containers",
			Value: "microshift-okd-",
		},
		&cli.StringFlag{
			Name:  "image",
			Usage: "Container image to run for cluster nodes",
			Value: "microshift-okd",
		},
		&cli.StringFlag{
			Name:  "lvm-disk",
			Usage: "Path to the sparse disk image used for TopoLVM",
			Value: "/var/lib/microshift-okd/lvmdisk.image",
		},
		&cli.StringFlag{
			Name:  "extra-config",
			Usage: "Path to the generated MicroShift extra config file",
			Value: "/var/lib/microshift-okd/custom_config.yaml",
		},
		&cli.StringFlag{
			Name:  "lvm-volsize",
			Usage: "Size of the sparse disk image used for TopoLVM",
			Value: "1G",
		},
		&cli.IntFlag{
			Name:  "api-server-port",
			Usage: "Host port to expose the Kubernetes API server on",
			Value: 6443,
			Validator: func(v int) error {
				if v < 1 || v > 65535 {
					return fmt.Errorf("api server port must be between 1 and 65535: %d", v)
				}
				return nil
			},
		},
		&cli.StringFlag{
			Name:  "vg-name",
			Usage: "Volume group name used for TopoLVM backend setup",
			Value: "myvg1",
		},
		&cli.BoolFlag{
			Name:  "expose-kubeapi",
			Usage: "Expose the Kubernetes API server on the host",
			Value: true,
		},
	}

	newManager := func(cmd *cli.Command) (*cluster.Manager, error) {
		cfg, err := cluster.NewConfigFromCommand(cmd)
		if err != nil {
			return nil, err
		}
		return cluster.NewManager(cfg, logger, runner), nil
	}

	return &cli.Command{
		Name:  "dfmicro",
		Usage: "Manage dfmicro clusters",
		Flags: flags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowAppHelp(cmd)
		},
		Commands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Create a cluster",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					manager, err := newManager(cmd)
					if err != nil {
						return err
					}
					return manager.Create(ctx)
				},
			},
			{
				Name:  "start",
				Usage: "Start cluster containers",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					manager, err := newManager(cmd)
					if err != nil {
						return err
					}
					return manager.Start(ctx)
				},
			},
			{
				Name:  "stop",
				Usage: "Stop cluster containers",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					manager, err := newManager(cmd)
					if err != nil {
						return err
					}
					return manager.Stop(ctx)
				},
			},
			{
				Name:  "delete",
				Usage: "Delete cluster containers and storage",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					manager, err := newManager(cmd)
					if err != nil {
						return err
					}
					return manager.Delete(ctx)
				},
			},
			{
				Name:  "status",
				Usage: "Show cluster status",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					manager, err := newManager(cmd)
					if err != nil {
						return err
					}
					return manager.Status(ctx)
				},
			},
			{
				Name:  "healthy",
				Usage: "Check cluster health",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					manager, err := newManager(cmd)
					if err != nil {
						return err
					}
					return manager.Healthy(ctx)
				},
			},
			{
				Name:  "ready",
				Usage: "Check cluster readiness",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					manager, err := newManager(cmd)
					if err != nil {
						return err
					}
					return manager.Ready(ctx)
				},
			},
		},
	}
}
