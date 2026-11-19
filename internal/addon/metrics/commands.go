package metrics

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
	return &cli.Command{
		Name:   "metrics",
		Usage:  "Manage Kubernetes resource metrics",
		Action: support.UnknownSubcommand,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "cluster", Usage: "Cluster name", Value: rootconfig.Load().Name},
		},
		Commands: []*cli.Command{
			{
				Name:  "install",
				Usage: "Install Metrics Server",
				Action: metricsAction(logger, runner, func(ctx context.Context, m *metrics) error {
					return m.install(ctx)
				}),
			},
			{
				Name:  "uninstall",
				Usage: "Remove Metrics Server",
				Action: metricsAction(logger, runner, func(ctx context.Context, m *metrics) error {
					return m.uninstall(ctx)
				}),
			},
		},
	}
}

func metricsAction(logger *slog.Logger, runner execx.Runner, fn func(context.Context, *metrics) error) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		clusterName := cmd.String("cluster")
		kubeconfig, err := rootconfig.Kubeconfig(clusterName)
		if err != nil {
			return fmt.Errorf("could not load kubeconfig for cluster %q: %w", clusterName, err)
		}
		m := &metrics{logger: logger, runner: runner, clusterName: clusterName, kubeconfig: kubeconfig}
		return fn(ctx, m)
	}
}
