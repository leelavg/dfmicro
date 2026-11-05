package odr

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "odr",
		Usage: "Manage OpenShift Data Foundation disaster recovery",
		UsageText: `Manage ODF disaster recovery on MicroShift.

Note: --cluster applies to all subcommands and must come before the subcommand name.`,
		Action: support.UnknownSubcommand,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "cluster", Usage: "Cluster name to resolve kubeconfig from", Value: rootconfig.Load().Name},
		},
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Install the ODF Multicluster Orchestrator",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "catalog-image", Usage: "ODF catalog source image"},
					&cli.StringFlag{Name: "channel", Usage: "Subscription channel (e.g. stable-5.0)"},
					&cli.StringFlag{
						Name:      "version",
						Usage:     "OCP version in X.Y.Z format (e.g. 5.0.0)",
						Validator: support.ValidateVersionFormat,
					},
				},
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					for _, name := range []string{"catalog-image", "channel", "version"} {
						if cmd.String(name) == "" {
							return ctx, fmt.Errorf("--%s is required", name)
						}
					}
					return ctx, nil
				},
				Action: odrAction(logger, runner, func(ctx context.Context, cmd *cli.Command, o *odr) error {
					return o.install(ctx, cmd.String("catalog-image"), cmd.String("channel"), cmd.String("version"))
				}),
			},
			{
				Name:      "configure",
				Usage:     "Configure S3 and managed clusters for ODR",
				UsageText: `Requires local-s3 attached to the hub and both managed clusters. Creates the shared dfmicro-odr bucket.`,
				Flags: []cli.Flag{
					&cli.StringSliceFlag{Name: "managed-cluster", Usage: "ManagedCluster name (repeatable or comma separated)"},
					&cli.StringFlag{
						Name:  "type",
						Usage: "MirrorPeer type: async (RDR) or sync (MDR)",
						Validator: func(value string) error {
							if value == "" {
								return nil
							}
							if value != "async" && value != "sync" {
								return fmt.Errorf("--type must be async or sync")
							}
							return nil
						},
					},
				},
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					if err := validateManagedClusters(cmd.StringSlice("managed-cluster")); err != nil {
						return ctx, err
					}
					if cmd.String("type") == "" {
						return ctx, fmt.Errorf("--type is required")
					}
					return ctx, nil
				},
				Action: odrAction(logger, runner, func(ctx context.Context, cmd *cli.Command, o *odr) error {
					return o.configure(ctx, cmd.StringSlice("managed-cluster"), cmd.String("type"))
				}),
			},
		},
	}
}

func odrAction(logger *slog.Logger, runner execx.Runner, fn func(context.Context, *cli.Command, *odr) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		clusterName := cmd.String("cluster")
		kubeconfig, err := rootconfig.Kubeconfig(clusterName)
		if err != nil {
			return fmt.Errorf("could not load kubeconfig for cluster %q: %w", clusterName, err)
		}
		o := &odr{logger: logger, runner: runner, clusterName: clusterName, kubeconfig: kubeconfig}
		return fn(ctx, cmd, o)
	}
}

func validateManagedClusters(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("managed cluster name cannot be empty")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate managed cluster name: %s", name)
		}
		seen[name] = struct{}{}
	}
	if len(seen) != 2 {
		return fmt.Errorf("exactly two --managed-cluster names are required")
	}
	return nil
}
