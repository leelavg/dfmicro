package odf

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"

	"dfmicro/internal/cluster"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

// basic X.Y.Z check, use a semver library if stricter validation is needed
var reVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

var defaultName = rootconfig.Load().Name

func odfAction(logger *slog.Logger, runner execx.Runner, fn func(context.Context, *cli.Command, *odf) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		useKubectl := cmd.Bool("kubectl")
		kubeconfig := cmd.String("kubeconfig")
		if kubeconfig == "" {
			kc, err := cluster.Kubeconfig(cmd.String("name"))
			if err != nil {
				return fmt.Errorf("could not load kubeconfig for cluster %q: %w", cmd.String("name"), err)
			}
			kubeconfig = kc
		}
		o := newOdf(logger, runner, useKubectl, kubeconfig)
		return fn(ctx, cmd, o)
	}
}

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:      "odf",
		Usage:     "Manage OpenShift Data Foundation",
		UsageText: "dfmicro addon odf [options] <command>  # options must precede the subcommand",
		Action:    support.UnknownSubcommand,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "kubectl",
				Usage: "Use kubectl instead of oc",
			},
		},
		MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
			{
				Required: true,
				Flags: [][]cli.Flag{
					{&cli.StringFlag{Name: "name", Usage: "Cluster name to resolve kubeconfig from", Value: defaultName}},
					{&cli.StringFlag{Name: "kubeconfig", Usage: "Path to kubeconfig file"}},
				},
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Install ODF operator and shim resources",
				Description: "The host kernel must have rbd and ceph modules loaded before installing ODF.\n" +
					"Install the extra kernel modules package for your distro and run:\n" +
					"  sudo modprobe rbd ceph",
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
				Name:   "modules",
				Usage:  "Manage ODF kernel module auto-load configuration",
				Action: support.UnknownSubcommand,
				Commands: []*cli.Command{
					{
						Name:  "load",
						Usage: "Configure rbd, ceph, nbd modules to load at boot",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return loadModules(ctx, logger, runner)
						},
					},
					{
						Name:  "unload",
						Usage: "Remove ODF kernel module auto-load configuration",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return unloadModules(ctx, logger, runner)
						},
					},
				},
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
