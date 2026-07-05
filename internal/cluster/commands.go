package cluster

import (
	"context"
	"fmt"
	"log/slog"

	"dfmicro/internal/execx"

	"github.com/urfave/cli/v3"
)

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
		},
		&cli.StringFlag{
			Name:  "lvm-volsize",
			Usage: "Size of the sparse disk image used for TopoLVM",
		},
		&cli.IntFlag{
			Name:  "api-server-port",
			Usage: "Host port to expose the Kubernetes API server on",
			Validator: func(v int) error {
				if v < 1 || v > 65535 {
					return fmt.Errorf("api server port must be between 1 and 65535: %d", v)
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
		Flags: clusterFlags(),
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
		Commands: []*cli.Command{
			{
				Name:    "list",
				Aliases: []string{"ls"},
				Usage:   "List all dfmicro clusters",
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
		},
	}
}
