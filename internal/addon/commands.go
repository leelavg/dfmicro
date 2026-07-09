package addon

import (
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
		Description: `Addons extend a running MicroShift cluster with additional capabilities.
Each addon manages its own install, configure, and uninstall lifecycle.

Available addons:
  odf   OpenShift Data Foundation (Ceph-based block, file, and object storage)`,
		Action: support.UnknownSubcommand,
		Commands: []*cli.Command{
			odf.Command(logger, runner),
		},
	}
}
