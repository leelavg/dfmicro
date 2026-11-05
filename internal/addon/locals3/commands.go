package locals3

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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
				Name:      "configure",
				Usage:     "Publish or remove local-s3 in clusters",
				UsageText: `Cluster names can be repeated or comma-separated.`,
				MutuallyExclusiveFlags: []cli.MutuallyExclusiveFlags{
					{
						Flags: [][]cli.Flag{
							{&cli.StringSliceFlag{Name: "attach-to", Usage: "Attach local-s3 to these dfmicro clusters"}},
							{&cli.StringSliceFlag{Name: "detach-from", Usage: "Remove local-s3 from these dfmicro clusters"}},
						},
						Required: true,
					},
				},
				Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
					clusters := cmd.StringSlice("attach-to")
					if len(clusters) == 0 {
						clusters = cmd.StringSlice("detach-from")
					}
					return ctx, validateClusterNames(clusters)
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return m.configure(ctx, cmd.StringSlice("attach-to"), cmd.StringSlice("detach-from"))
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

func validateClusterNames(clusters []string) error {
	seen := make(map[string]struct{}, len(clusters))
	for _, cluster := range clusters {
		cluster = strings.TrimSpace(cluster)
		if cluster == "" {
			return fmt.Errorf("cluster name cannot be empty")
		}
		if _, ok := seen[cluster]; ok {
			return fmt.Errorf("duplicate cluster name: %s", cluster)
		}
		seen[cluster] = struct{}{}
	}
	if len(seen) == 0 {
		return fmt.Errorf("at least one cluster name is required")
	}
	return nil
}
