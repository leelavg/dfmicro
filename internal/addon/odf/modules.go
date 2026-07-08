package odf

import (
	"context"
	"fmt"
	"log/slog"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

const odfModulesConf = "rbd\nceph\nnbd\n"
const odfModulesPath = "/etc/modules-load.d/dfmicro-odf.conf"

func loadModules(ctx context.Context, logger *slog.Logger, runner execx.Runner) error {
	logger.Info("configuring ODF kernel modules for auto-load at boot", "path", odfModulesPath)
	if err := support.WritePrivileged(ctx, runner, odfModulesPath, odfModulesConf, 0644); err != nil {
		return fmt.Errorf("failed to write modules-load.d config: %w", err)
	}
	logger.Info("loading kernel modules for current session")
	if _, err := support.RunPrivileged(ctx, runner, "modprobe", "rbd", "ceph", "nbd"); err != nil {
		return fmt.Errorf("failed to load kernel modules: %w", err)
	}
	logger.Info("kernel modules (rbd, ceph, nbd) loaded and configured for auto-load at boot")
	return nil
}

func unloadModules(ctx context.Context, logger *slog.Logger, runner execx.Runner) error {
	logger.Info("removing ODF kernel module auto-load configuration", "path", odfModulesPath)
	if _, err := support.RunPrivileged(ctx, runner, "rm", "-f", odfModulesPath); err != nil {
		return fmt.Errorf("failed to remove modules-load.d config: %w", err)
	}
	logger.Info("unloading kernel modules from current session")
	if _, err := support.RunPrivileged(ctx, runner, "modprobe", "-r", "nbd", "ceph", "rbd"); err != nil {
		logger.Warn("failed to unload kernel modules", "error", err)
	}
	return nil
}
