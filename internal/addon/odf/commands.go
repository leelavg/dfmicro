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
		Name:  "odf",
		Usage: "Manage OpenShift Data Foundation on a MicroShift cluster",
		Description: `Installs, configures, and removes ODF (Ceph-based storage) on a single-node MicroShift cluster.
ODF requires kernel modules (rbd, ceph, nbd) and a dedicated Ceph catalog image.

Note: flags like --name and --kubeconfig apply to all subcommands and must come before the subcommand name.

Verified on: Linux (Fedora / RHEL). Not tested on macOS.

Examples:
  dfmicro addon odf --name cluster install --catalog-image ... --channel stable-4.16 --version 4.16.0
  dfmicro addon odf --name cluster configure
  dfmicro addon odf --name cluster uninstall --attempt`,
		UsageText: "dfmicro addon odf [--name NAME | --kubeconfig PATH] [--kubectl] <command>",
		Action:    support.UnknownSubcommand,
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "kubectl",
				Usage: "Use kubectl instead of oc for cluster operations",
			},
		},
		MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
			{
				Required: true,
				Flags: [][]cli.Flag{
					{&cli.StringFlag{Name: "name", Usage: "Cluster name to resolve kubeconfig from", Value: defaultName}},
					{&cli.StringFlag{Name: "kubeconfig", Usage: "Path to an existing kubeconfig file"}},
				},
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Install ODF operator, shim CRDs, RBAC, and catalog source",
				Description: `Applies numbered shim resources (CRDs, RBAC, oauth, catalog source) in order, then
creates the ODF subscription and waits for the operator to reach a ready state.

Prerequisites:
  - Kernel modules rbd, ceph, and nbd must be loaded on the host before installing.
  - Install the extra kernel modules package for your distro, then run:
      dfmicro addon odf modules load        # writes /etc/modules-load.d and loads for current session
  - A Ceph catalog image compatible with the target OCP version is required.

Examples:
  dfmicro addon odf --name cluster install \
    --catalog-image quay.io/example/odf-catalog:v4.16 \
    --channel stable-4.16 \
    --version 4.16.0`,
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "catalog-image",
						Usage:    "Catalog source image containing the ODF operator bundle",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "channel",
						Usage:    "OLM subscription channel (e.g. stable-4.16)",
						Required: true,
					},
					&cli.StringSliceFlag{
						Name:  "sub-name",
						Usage: "OLM subscription name(s) to create (repeatable)",
						Value: []string{"odf-operator"},
					},
					&cli.StringFlag{
						Name:     "version",
						Usage:    "OCP version string in X.Y.Z format (e.g. 4.16.0) used to select the correct shim resources",
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
				Usage: "Apply SINGLE_NODE StorageCluster CR and label the storage node",
				Description: `Discovers the node name via jsonpath, applies the SINGLE_NODE patch, labels the node
for ODF placement, and applies the StorageCluster resource. Run this after 'install' once
the operator CSV reaches Succeeded.`,
				Action: odfAction(logger, runner, func(ctx context.Context, _ *cli.Command, o *odf) error {
					return o.Configure(ctx)
				}),
			},
			{
				Name:        "modules",
				Usage:       "Manage ODF kernel module auto-load configuration",
				Description: "Writes or removes /etc/modules-load.d/dfmicro-odf.conf (rbd, ceph, nbd) and loads or unloads the modules for the current session.",
				Action:      support.UnknownSubcommand,
				Commands: []*cli.Command{
					{
						Name:  "load",
						Usage: "Write modules-load.d config and load rbd, ceph, nbd for the current session",
						Description: `Writes /etc/modules-load.d/dfmicro-odf.conf so the modules load automatically at boot,
then runs 'modprobe rbd ceph nbd' to load them immediately.

Requires passwordless sudo (via 'dfmicro ops sudoers create') or will prompt for a password.
Linux only. Not applicable on macOS.`,
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return loadModules(ctx, logger, runner)
						},
					},
					{
						Name:  "unload",
						Usage: "Remove modules-load.d config and unload rbd, ceph, nbd from the current session",
						Description: `Removes /etc/modules-load.d/dfmicro-odf.conf, then runs 'modprobe -r nbd ceph rbd'.
Unload failure is non-fatal: modules in use by a running Ceph cluster will not be removed.`,
						Action: func(ctx context.Context, cmd *cli.Command) error {
							return unloadModules(ctx, logger, runner)
						},
					},
				},
			},
			{
				Name:  "uninstall",
				Usage: "Uninstall ODF operator and all associated resources",
				Description: `By default, prints the delete commands without running them so you can review them first.
Pass --attempt to actually execute the cleanup (best-effort: errors are logged but do not stop the sequence).

Cleanup sequence:
  1. Remove finalizers from csiaddonsnodes.csiaddons.openshift.io resources
  2. Delete mutatingwebhookconfiguration csv.odf.openshift.io
  3. Delete subscriptions, CSVs, CatalogSource, and shim resources
  4. Delete the openshift-storage namespace
  5. Remove node labels applied by 'configure'

Examples:
  dfmicro addon odf --name cluster uninstall             # dry-run: print commands
  dfmicro addon odf --name cluster uninstall --attempt   # execute cleanup`,
				Flags: []cli.Flag{
					&cli.BoolFlag{
						Name:  "attempt",
						Usage: "Execute the delete commands instead of printing them (best-effort)",
					},
				},
				Action: odfAction(logger, runner, func(ctx context.Context, cmd *cli.Command, o *odf) error {
					return o.Uninstall(ctx, cmd.Bool("attempt"))
				}),
			},
		},
	}
}
