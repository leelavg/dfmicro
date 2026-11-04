package ocm

import (
	"context"
	"log/slog"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	o := &ocm{logger: logger, runner: runner}
	return &cli.Command{
		Name:  "ocm",
		Usage: "Manage Open Cluster Management",
		UsageText: `Use --cluster before the subcommand to select its kubeconfig.

Example:
  dfmicro addon ocm --cluster ocm install`,
		Action: support.UnknownSubcommand,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "cluster", Usage: "Cluster to install OCM on", Value: rootconfig.Load().Name},
		},
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Install OCM hub components",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return o.install(ctx, cmd.String("cluster"))
				},
			},
		},
	}
}
