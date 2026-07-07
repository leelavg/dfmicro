package addon

import (
	"context"
	"log/slog"

	"dfmicro/internal/addon/odf"
	"dfmicro/internal/execx"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "addon",
		Usage: "Manage cluster addons",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
		Commands: []*cli.Command{
			odf.Command(logger, runner),
		},
	}
}
