package cluster

import (
	"context"
	"fmt"
	"log/slog"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

var defaultRootConfig = rootconfig.Load()

func nameFlag() cli.Flag {
	return &cli.StringFlag{
		Name:  "name",
		Usage: "Cluster name",
		Value: "cluster",
	}
}

func createFlags() []cli.Flag {
	return []cli.Flag{
		nameFlag(),
		&cli.StringFlag{
			Name:  "image",
			Usage: "Container image to run for cluster nodes",
			Value: defaultRootConfig.Image,
		},
		&cli.StringFlag{
			Name:  "lvm-volsize",
			Usage: "Size of the sparse disk image used for TopoLVM",
			Value: defaultRootConfig.LVMVolSize,
		},
		&cli.IntFlag{
			Name:  "api-server-port",
			Usage: "Host port to expose the Kubernetes API server on",
			Value: defaultRootConfig.APIServerPort,
			Validator: func(v int) error {
				if v < 1024 || v > 65535 {
					return fmt.Errorf("api server port must be between 1024 and 65535: %d", v)
				}
				return nil
			},
		},
		&cli.BoolFlag{
			Name:  "no-expose-kubeapi",
			Usage: "Disable exposing the Kubernetes API server on the host",
		},
		&cli.Float32Flag{
			Name:  "overprovision-ratio",
			Usage: "TopoLVM thin pool overprovision ratio",
			Value: defaultRootConfig.OverprovisionRatio,
		},
		&cli.BoolFlag{
			Name:  "no-share-host-containers",
			Usage: "Disable mounting host /var/lib/containers for image reuse",
		},
	}
}

func clusterFlags() []cli.Flag {
	return []cli.Flag{nameFlag()}
}

func commandAction(logger *slog.Logger, runner execx.Runner, fn func(context.Context, *Manager) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		cfg, err := newConfigFromCommand(cmd)
		if err != nil {
			return err
		}
		return fn(ctx, NewManager(cfg, logger, runner))
	}
}

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "cluster",
		Usage: "Manage cluster lifecycle",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
		Commands: []*cli.Command{
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Usage:   "List all " + support.BinaryName + " clusters",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return listAll(ctx, logger, runner)
				},
			},
			{
				Name:   "create",
				Usage:  "Create a cluster, wait until ready, and write kubeconfig",
				Flags:  createFlags(),
				Action: commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Create(ctx) }),
			},
			{
				Name:    "delete",
				Aliases: []string{"rm"},
				Usage:   "Delete cluster containers and storage",
				Flags:   clusterFlags(),
				Action:  commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Delete(ctx) }),
			},
			{
				Name:   "start",
				Usage:  "Start cluster containers",
				Flags:  clusterFlags(),
				Action: commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Start(ctx) }),
			},
			{
				Name:   "stop",
				Usage:  "Stop cluster containers",
				Flags:  clusterFlags(),
				Action: commandAction(logger, runner, func(ctx context.Context, manager *Manager) error { return manager.Stop(ctx) }),
			},
			{
				Name:  "config",
				Usage: "Print saved cluster config",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Usage:    "Cluster name",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return printClusterConfig(cmd.String("name"))
				},
			},
			{
				Name:  "exec",
				Usage: "Execute a shell in a running container",
				Flags: []cli.Flag{
					nameFlag(),
					&cli.StringFlag{
						Name:  "container",
						Usage: "Container name (defaults to first running container)",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg, err := readClusterConfig(cmd.String("name"))
					if err != nil {
						return err
					}
					manager := NewManager(cfg, logger, runner)
					return manager.Exec(ctx, cmd.String("container"))
				},
			},
		},
	}
}
