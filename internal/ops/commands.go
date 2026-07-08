package ops

import (
	"context"
	"log/slog"

	"dfmicro/internal/execx"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "ops",
		Usage: "Operational utilities for running clusters",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
		Commands: []*cli.Command{
			resourcesCommand(runner),
			sudoersCommand(logger, runner),
		},
	}
}
