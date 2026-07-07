package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"

	"dfmicro/internal/addon"
	"dfmicro/internal/buildinfo"
	"dfmicro/internal/cluster"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/devlog"
	"dfmicro/internal/docs"
	"dfmicro/internal/execx"
	"dfmicro/internal/perms"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func devlogCommand() *cli.Command {
	return &cli.Command{
		Name:   "devlog",
		Usage:  "Print development log",
		Hidden: true,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, err := os.Stdout.WriteString(devlog.Content)
			return err
		},
	}
}

func docsCommand() *cli.Command {
	return &cli.Command{
		Name:  "docs",
		Usage: "Print full command reference as markdown",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			_, err := os.Stdout.WriteString(docs.CLI)
			return err
		},
	}
}

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
		Name:                  support.BinaryName,
		Usage:                 "Manage " + support.BinaryName + " clusters",
		Version:               buildinfo.String(),
		EnableShellCompletion: true,
		Action:                support.UnknownSubcommand,
		Commands: []*cli.Command{
			addon.Command(logger, runner),
			configCommand(),
			docsCommand(),
			devlogCommand(),
			cluster.Command(logger, runner),
			perms.Command(logger, runner),
		},
	}

	sortAll(cmd)
	return cmd
}
