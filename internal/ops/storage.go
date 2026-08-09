package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dfmicro/internal/execx"

	"github.com/urfave/cli/v3"
)

func storageCommand(runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "storage",
		Usage: "Show storage paths for all dfmicro clusters",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			result, err := execx.RunPodmanCommand(ctx, runner, "ps", "--all", "--filter", "label=created-by=dfmicro", "--format", "json")
			if err != nil {
				return err
			}

			type Container struct {
				Names []string `json:"Names"`
			}
			var containers []Container
			if err := json.Unmarshal([]byte(result.Stdout), &containers); err != nil {
				return err
			}

			for _, c := range containers {
				if len(c.Names) == 0 {
					continue
				}
				name := c.Names[0]

				result, err := execx.RunPodmanCommand(ctx, runner, "inspect", name, "--format", "{{.GraphDriver.Data.MergedDir}}")
				if err != nil {
					fmt.Printf("%s\terror\n", name)
					continue
				}
				fmt.Printf("%s\t%s\n", name, strings.TrimSpace(result.Stdout))
			}
			return nil
		},
	}
}
