package ops

import (
	"context"
	"errors"
	"log/slog"

	"dfmicro/internal/execx"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) (*cli.Command, func(context.Context) error) {
	routes := &routeDNS{logger: logger, runner: runner}
	clusterSet := &clusterSetDNS{logger: logger, runner: runner}
	command := &cli.Command{
		Name:  "ops",
		Usage: "Operational utilities for running clusters",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
		Commands: []*cli.Command{
			resourcesCommand(logger, runner),
			routesCommand(logger, runner),
			sudoersCommand(logger, runner),
			storageCommand(runner),
			clusterSetCommand(clusterSet),
		},
	}
	return command, func(ctx context.Context) error {
		return errors.Join(routes.reconcile(ctx), clusterSet.reconcile(ctx))
	}
}
