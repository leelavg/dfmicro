package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"sort"
	"strings"

	"dfmicro/internal/buildinfo"
	"dfmicro/internal/cluster"
	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/docs"
	"dfmicro/internal/execx"
	"dfmicro/internal/perms"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

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
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if name := cmd.Args().First(); name != "" {
				var matches []string
				for _, sub := range cmd.Commands {
					if strings.HasPrefix(sub.Name, name[:1]) {
						matches = append(matches, sub.Name)
					}
				}
				if len(matches) > 0 {
					return fmt.Errorf("unknown command %q, did you mean: %s", name, strings.Join(matches, ", "))
				}
				return fmt.Errorf("unknown command %q", name)
			}
			return cli.ShowAppHelp(cmd)
		},
		Commands: []*cli.Command{
			configCommand(),
			docsCommand(),
			cluster.Command(logger, runner),
			perms.Command(logger, runner),
		},
	}

	sortAll(cmd)
	return cmd
}
