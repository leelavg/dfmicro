package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"

	"dfmicro/internal/cluster"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/perms"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func configCommand() *cli.Command {
	return &cli.Command{
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
	}
}

func sortAll(cmd *cli.Command) {
	sort.Sort(cli.FlagsByName(cmd.Flags))
	slices.SortFunc(cmd.Commands, func(a, b *cli.Command) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, subCmd := range cmd.Commands {
		sortAll(subCmd)
	}
}

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	cmd := &cli.Command{
		Name:  support.BinaryName,
		Usage: "Manage " + support.BinaryName + " clusters",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowAppHelp(cmd)
		},
		Commands: []*cli.Command{
			configCommand(),
			cluster.Command(logger, runner),
			perms.Command(logger, runner),
		},
	}

	sortAll(cmd)
	return cmd
}
