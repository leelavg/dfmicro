package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"dfmicro/internal/cluster"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"

	"github.com/urfave/cli/v3"
)

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "dfmicro",
		Usage: "Manage dfmicro clusters",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowAppHelp(cmd)
		},
		Commands: []*cli.Command{
			{
				Name:  "config",
				Usage: "Print top-level embedded config",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					cfg := rootconfig.Load()

					data, err := json.MarshalIndent(cfg, "", "  ")
					if err != nil {
						return err
					}
					data = append(data, '\n')

					_, err = os.Stdout.Write(data)
					return err
				},
			},
			cluster.Command(logger, runner),
		},
	}
}
