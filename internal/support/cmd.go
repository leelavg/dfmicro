package support

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"
)

func UnknownSubcommand(ctx context.Context, cmd *cli.Command) error {
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
	return cli.ShowSubcommandHelp(cmd)
}

func SortCommand(cmd *cli.Command) {
	sort.Sort(cli.FlagsByName(cmd.Flags))
	slices.SortFunc(cmd.Commands, func(a, b *cli.Command) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, subCmd := range cmd.Commands {
		SortCommand(subCmd)
	}
}
