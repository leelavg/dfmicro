package ocm

import (
	"context"
	"fmt"
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
		UsageText: `Use --cluster before the subcommand to select the hub.

Example:
  dfmicro addon ocm --cluster hub install --version 5.0.0
  dfmicro addon ocm --cluster hub configure --spoke spoke --addon governance-policy-framework,argocd
  dfmicro addon ocm --cluster hub configure --addon application-manager
  dfmicro addon ocm --list-addons`,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("list-addons") {
				return o.listAddons()
			}
			return support.UnknownSubcommand(ctx, cmd)
		},
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "cluster", Usage: "Hub cluster for OCM operations", Value: rootconfig.Load().Name},
			&cli.BoolFlag{Name: "list-addons", Usage: "List supported OCM addon setup for hub and spokes"},
		},
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Install OCM hub components",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:      "version",
						Usage:     "OCP version in X.Y.Z format (e.g. 5.0.0)",
						Required:  true,
						Validator: support.ValidateVersionFormat,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return o.install(ctx, cmd.String("cluster"), cmd.String("version"))
				},
			},
			{
				Name:  "configure",
				Usage: "Join spokes and configure OCM addons",
				UsageText: `Without --addon, configure only joins the selected spokes.
With --addon and no --spoke, it installs the addon on the hub only.

Example:
  dfmicro addon ocm --cluster hub configure --spoke spoke1,spoke2 --addon governance-policy-framework,application-manager,argocd`,
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					spokes := cmd.StringSlice("spoke")
					addons := cmd.StringSlice("addon")
					if len(spokes) == 0 && len(addons) == 0 {
						return ctx, fmt.Errorf("supply at least one --spoke or --addon")
					}
					if err := validateOCMSpokes(spokes); err != nil {
						return ctx, err
					}
					if err := validateOCMAddons(addons); err != nil {
						return ctx, err
					}
					return ctx, nil
				},
				Flags: []cli.Flag{
					&cli.StringSliceFlag{
						Name:  "spoke",
						Usage: "Cluster to join as a spoke (repeatable or comma separated)",
					},
					&cli.StringSliceFlag{
						Name:  "addon",
						Usage: "Addon to configure on the hub and selected spokes",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return o.configure(ctx, cmd.String("cluster"), cmd.StringSlice("spoke"), cmd.StringSlice("addon"))
				},
			},
		},
	}
}
