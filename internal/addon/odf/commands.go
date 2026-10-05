package odf

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

// basic X.Y.Z check, use a semver library if stricter validation is needed
var reVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

var defaultCluster = rootconfig.Load().Name

func odfAction(logger *slog.Logger, runner execx.Runner, fn func(context.Context, *cli.Command, *odf) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		useKubectl := cmd.Bool("kubectl")
		kubeconfig := cmd.String("kubeconfig")
		if kubeconfig == "" {
			kc, err := rootconfig.Kubeconfig(cmd.String("cluster"))
			if err != nil {
				return fmt.Errorf("could not load kubeconfig for cluster %q: %w", cmd.String("cluster"), err)
			}
			kubeconfig = kc
		}
		o := newOdf(logger, runner, useKubectl, kubeconfig, cmd.String("cluster"))
		return fn(ctx, cmd, o)
	}
}

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "odf",
		Usage: "Manage OpenShift Data Foundation on a MicroShift cluster",
		UsageText: `Manage ODF lifecycle on MicroShift. Verified on Linux, not tested on macOS.

Note: --cluster and --kubeconfig apply to all subcommands and must come before the subcommand name.`,
		Action: support.UnknownSubcommand,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "kubectl",
				Usage: "Use kubectl instead of oc for cluster operations",
			},
		},
		MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
			{
				Flags: [][]cli.Flag{
					{&cli.StringFlag{Name: "cluster", Usage: "Cluster name to resolve kubeconfig from", Value: defaultCluster}},
					{&cli.StringFlag{Name: "kubeconfig", Usage: "Path to an existing kubeconfig file"}},
				},
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Install ODF and required shim resources",
				UsageText: `Requires rbd, ceph, nbd kernel modules loaded on the host. Run 'dfmicro addon odf modules load' first.

Example:
  dfmicro addon odf install --catalog-image quay.io/example/catalog:v4.16 --channel stable-4.16 --version 4.16.0`,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "shims",
						Usage: "Apply only the shim CRDs",
					},
					&cli.StringFlag{
						Name:  "catalog-image",
						Usage: "Catalog source image",
					},
					&cli.StringFlag{
						Name:  "channel",
						Usage: "Subscription channel (e.g. stable-4.16)",
					},
					&cli.StringSliceFlag{
						Name:  "sub-name",
						Usage: "Subscription name (repeatable)",
						Value: []string{"odf-operator"},
					},
					&cli.StringFlag{
						Name:  "version",
						Usage: "OCP version in X.Y.Z format (e.g. 4.16.0)",
						Validator: func(v string) error {
							if v == "" {
								return nil
							}
							if !reVersion.MatchString(v) {
								return fmt.Errorf("version %q must be in X.Y.Z format", v)
							}
							return nil
						},
					},
				},
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					if cmd.Bool("shims") {
						for _, cflag := range []string{"catalog-image", "channel", "version"} {
							if cmd.IsSet(cflag) {
								return ctx, fmt.Errorf("--shims cannot be combined with --%s", cflag)
							}
						}
						return ctx, nil
					}
					for _, cflag := range []string{"catalog-image", "channel", "version"} {
						if cmd.String(cflag) == "" {
							return ctx, fmt.Errorf("--%s is required unless --shims is set", cflag)
						}
					}
					return ctx, nil
				},
				Action: odfAction(logger, runner, func(ctx context.Context, cmd *cli.Command, o *odf) error {
					clusterCIDR := rootconfig.Load().ClusterCIDR
					serviceCIDR := rootconfig.Load().ServiceCIDR
					if cmd.String("kubeconfig") == "" {
						clusterConfig, err := rootconfig.ReadClusterConfig(cmd.String("cluster"))
						if err != nil {
							return fmt.Errorf("could not load cluster config for %q: %w", cmd.String("cluster"), err)
						}
						clusterCIDR = clusterConfig.ClusterCIDR
						serviceCIDR = clusterConfig.ServiceCIDR
					}
					return o.install(ctx, installConfig{
						catalogImage: cmd.String("catalog-image"),
						channel:      cmd.String("channel"),
						subNames:     cmd.StringSlice("sub-name"),
						version:      cmd.String("version"),
						shims:        cmd.Bool("shims"),
						baseDomain:   rootconfig.BaseDomain(cmd.String("cluster")),
						clusterCIDR:  clusterCIDR,
						serviceCIDR:  serviceCIDR,
					})
				}),
			},
			{
				Name:      "configure",
				Usage:     "Configure ODF to run on MicroShift",
				UsageText: `Run after 'install' once the operator CSV reaches Succeeded. Applies without retries and fails fast on any error.`,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "client",
						Usage: "Client-only mode",
					},
					&cli.BoolFlag{
						Name:  "include-cephfs",
						Usage: "Run CephFS and CSI Driver",
					},
					&cli.BoolFlag{
						Name:  "multi-node",
						Usage: "Configure ODF for a multi-node cluster",
					},
					&cli.BoolFlag{
						Name:  "hostnetwork",
						Usage: "Run ODF storage pods on the host network",
					},
					&cli.BoolFlag{
						Name:  "external-ceph",
						Usage: "Configure a Rook-managed Ceph provider",
					},
					&cli.StringFlag{
						Name:  "connect-to",
						Usage: "Source cluster. With --client, connects to an ODF provider. Otherwise, connects to a Rook Ceph provider",
					},
				},
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					if cmd.Bool("client") && cmd.Bool("external-ceph") {
						return ctx, fmt.Errorf("--client cannot be combined with --external-ceph")
					}
					if cmd.Bool("external-ceph") && cmd.String("connect-to") != "" {
						return ctx, fmt.Errorf("--external-ceph cannot be combined with --connect-to")
					}
					return ctx, nil
				},
				Action: odfAction(logger, runner, func(ctx context.Context, cmd *cli.Command, o *odf) error {
					return o.configure(ctx, configureConfig{
						clientOnly:    cmd.Bool("client"),
						includeCephFS: cmd.Bool("include-cephfs"),
						multiNode:     cmd.Bool("multi-node"),
						hostNetwork:   cmd.Bool("hostnetwork"),
						externalCeph:  cmd.Bool("external-ceph"),
						connectTo:     cmd.String("connect-to"),
					})
				}),
			},
			{
				Name:   "modules",
				Usage:  "Manage ODF kernel module auto-load configuration",
				Action: support.UnknownSubcommand,
				Commands: []*cli.Command{
					{
						Name:  "load",
						Usage: "Load rbd, ceph, nbd kernel modules and configure auto-load at boot",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return loadModules(ctx, logger, runner)
						},
					},
					{
						Name:  "unload",
						Usage: "Unload rbd, ceph, nbd kernel modules and remove auto-load config",
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return unloadModules(ctx, logger, runner)
						},
					},
				},
			},
			{
				Name:  "uninstall",
				Usage: "Uninstall ODF and all associated resources",
				UsageText: `Prints the cleanup commands by default. Pass --attempt to execute them (best-effort).

Examples:
  dfmicro addon odf uninstall            # dry-run: print commands
  dfmicro addon odf uninstall --attempt  # execute cleanup`,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "attempt",
						Usage: "Execute the delete commands instead of printing them (best-effort)",
					},
				},
				Action: odfAction(logger, runner, func(ctx context.Context, cmd *cli.Command, o *odf) error {
					return o.uninstall(ctx, cmd.Bool("attempt"))
				}),
			},
		},
	}
}
