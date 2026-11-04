package locals3

import (
	"context"
	"log/slog"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	m := &manager{logger: logger, runner: runner}
	return &cli.Command{
		Name:   "local-s3",
		Usage:  "Manage the standalone local S3 service",
		Action: support.UnknownSubcommand,
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Start the standalone local-s3 service",
				UsageText: `Starts a standalone container on dfmicro's default bridge network without publishing a host port. Data is kept in a named volume.
Requires a cluster-created default bridge network.

Use configure to publish a Service in a cluster.
The service allows anonymous access until accounts are configured.`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "image",
						Usage: "Container image for local-s3",
						Value: defaultImage(),
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return m.install(ctx, cmd.String("image"))
				},
			},
			{
				Name:  "configure",
				Usage: "Publish local-s3 in a cluster",
				UsageText: `--attach-to creates s3.default.svc pointing to local-s3 on the cluster's Podman network.
--detach-from removes that Service and EndpointSlice.`,
				MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
					{
						Flags: [][]cli.Flag{
							{&cli.StringFlag{Name: "attach-to", Usage: "Attach S3 Services to this dfmicro cluster"}},
							{&cli.StringFlag{Name: "detach-from", Usage: "Remove local-s3 Services from this dfmicro cluster"}},
						},
						Required: true,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					if cluster := cmd.String("attach-to"); cluster != "" {
						return m.attach(ctx, cluster)
					}
					return m.detach(ctx, cmd.String("detach-from"))
				},
			},
			{
				Name:  "uninstall",
				Usage: "Remove local-s3 and its cluster Services",
				UsageText: `Configured clusters must be reachable so their Services can be removed.
The data volume is preserved unless --cleanup is passed.`,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "cleanup",
						Usage: "Also delete the local-s3 data volume",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return m.uninstall(ctx, cmd.Bool("cleanup"))
				},
			},
		},
	}
}
