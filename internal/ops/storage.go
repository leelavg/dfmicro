package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

func storageCommand(runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "storage",
		Usage: "Show storage paths for all dfmicro clusters",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			result, err := support.RunPodmanPrivileged(ctx, runner, "ps", "--all", "--filter", "label=created-by=dfmicro", "--format", "json")
			if err != nil {
				return err
			}

			type container struct {
				Names []string `json:"Names"`
			}
			var containers []container
			if err := json.Unmarshal([]byte(result.Stdout), &containers); err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "CONTAINER\tSTORAGE PATH")
			for _, c := range containers {
				if len(c.Names) == 0 {
					continue
				}
				name := c.Names[0]

				result, err := support.RunPodmanPrivileged(ctx, runner, "inspect", name, "--format", "{{.GraphDriver.Data.MergedDir}}")
				if err != nil {
					fmt.Fprintf(w, "%s\terror\n", name)
					continue
				}
				fmt.Fprintf(w, "%s\t%s\n", name, strings.TrimSpace(result.Stdout))
			}
			return w.Flush()
		},
	}
}
