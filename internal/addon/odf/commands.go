package odf

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"

	"dfmicro/internal/cluster"
	"dfmicro/internal/execx"

	"github.com/urfave/cli/v3"
)

// basic X.Y.Z check; use a semver library if stricter validation is needed
var reVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func odfAction(logger *slog.Logger, runner execx.Runner, fn func(context.Context, *cli.Command, *odf) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		useKubectl := cmd.Bool("kubectl")
		kubeconfig := cmd.String("kubeconfig")
		if kubeconfig == "" {
			if name := cmd.String("name"); name != "" {
				kc, err := cluster.Kubeconfig(name)
				if err != nil {
					return fmt.Errorf("could not load kubeconfig for cluster %q: %w", name, err)
				}
				kubeconfig = kc
			}
		}
		o := newOdf(logger, runner, useKubectl, kubeconfig)
		return fn(ctx, cmd, o)
	}
}

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "odf",
		Usage: "Manage OpenShift Data Foundation",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "kubectl",
				Usage: "Use kubectl instead of oc",
			},
			&cli.StringFlag{
				Name:  "name",
				Usage: "Cluster name to resolve kubeconfig from",
			},
			&cli.StringFlag{
				Name:  "kubeconfig",
				Usage: "Path to kubeconfig file (overrides --name)",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Install ODF operator and shim resources",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "catalog-image",
						Usage:    "Catalog source image",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "channel",
						Usage:    "Subscription channel",
						Required: true,
					},
					&cli.StringSliceFlag{
						Name:  "sub-name",
						Usage: "Subscription name",
						Value: []string{"odf-operator"},
					},
					&cli.StringFlag{
						Name:     "version",
						Usage:    "OCP version (e.g. 4.16.0)",
						Required: true,
						Validator: func(v string) error {
							if !reVersion.MatchString(v) {
								return fmt.Errorf("version %q must be in X.Y.Z format", v)
							}
							return nil
						},
					},
				},
				Action: odfAction(logger, runner, func(ctx context.Context, cmd *cli.Command, o *odf) error {
					return o.Install(ctx, installConfig{
						CatalogImage: cmd.String("catalog-image"),
						Channel:      cmd.String("channel"),
						SubNames:     cmd.StringSlice("sub-name"),
						Version:      cmd.String("version"),
					})
				}),
			},
			{
				Name:  "configure",
				Usage: "Deploy StorageCluster after operator is ready",
				Action: odfAction(logger, runner, func(ctx context.Context, _ *cli.Command, o *odf) error {
					return o.Configure(ctx)
				}),
			},
			{
				Name:  "uninstall",
				Usage: "Uninstall ODF (prints commands by default)",
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "attempt",
						Usage: "Actually run the delete commands (best-effort)",
					},
				},
				Action: odfAction(logger, runner, func(ctx context.Context, cmd *cli.Command, o *odf) error {
					return o.Uninstall(ctx, cmd.Bool("attempt"))
				}),
			},
		},
	}
}
