package support

import (
	"context"
	"fmt"
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
