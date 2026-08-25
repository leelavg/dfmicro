package support

import (
	"context"
	"fmt"
	"net"
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

func ValidateIPv4PrivateCIDR(s string) error {
	ip, _, err := net.ParseCIDR(s)
	if err != nil {
		return fmt.Errorf("invalid CIDR: %w", err)
	}
	if ip.To4() == nil {
		return fmt.Errorf("only IPv4 subnets are supported")
	}
	if !ip.IsPrivate() {
		return fmt.Errorf("CIDR must be a private range")
	}
	return nil
}
