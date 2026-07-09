package addon

import (
	"context"
	"fmt"
	"log/slog"

	"dfmicro/internal/addon/odf"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "addon",
		Usage: "Manage cluster addons",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "list",
				Usage: "List available addons",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.Bool("list") {
				for _, sub := range cmd.Commands {
					if sub.Name == "help" {
						continue
					}
					fmt.Printf("  %-12s %s\n", sub.Name, sub.Usage)
				}
				return nil
			}
			return support.UnknownSubcommand(ctx, cmd)
		},
		Commands: []*cli.Command{
			odf.Command(logger, runner),
		},
	}
}
