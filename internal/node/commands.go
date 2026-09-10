package node

import (
	"context"
	"log/slog"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:      "node",
		Usage:     "Manage cluster nodes",
		UsageText: "Add worker nodes to existing cluster.",
		Action:    support.UnknownSubcommand,
		Commands: []*cli.Command{
			{
				Name:  "add",
				Usage: "add worker node to existing cluster",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "cluster",
						Usage:    "cluster name",
						Required: true,
					},
					&cli.BoolFlag{
						Name:  "force",
						Usage: "skip control plane readiness checks",
					},
					&cli.StringSliceFlag{
						Name:     "mount",
						Usage:    "Extra bind mount in Podman format: /host/path:/container/path[:opts] (repeatable)",
						Category: "Storage:",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					clusterName := cmd.String("cluster")
					return newManager(clusterName, logger, runner).add(ctx, cmd.Bool("force"), cmd.StringSlice("mount"))
				},
			},
			{
				Name:    "remove",
				Aliases: []string{"rm", "delete"},
				Usage:   "remove worker node from cluster",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "name",
						Usage:    "worker node name",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "cluster",
						Usage:    "cluster name",
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					clusterName := cmd.String("cluster")
					nodeName := cmd.String("name")
					return newManager(clusterName, logger, runner).remove(ctx, nodeName)
				},
			},
		},
	}
}
