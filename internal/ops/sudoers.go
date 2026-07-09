package ops

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"

	"github.com/urfave/cli/v3"
)

const sudoersTemplate = `# %[2]s - passwordless sudo for cluster management
# Created by: %[2]s ops sudoers create
%[1]s ALL=(root) NOPASSWD: /usr/bin/podman
%[1]s ALL=(root) NOPASSWD: /usr/sbin/losetup
%[1]s ALL=(root) NOPASSWD: /usr/sbin/vgcreate
%[1]s ALL=(root) NOPASSWD: /usr/sbin/lvcreate
%[1]s ALL=(root) NOPASSWD: /usr/sbin/lvremove
%[1]s ALL=(root) NOPASSWD: /usr/sbin/vgremove
%[1]s ALL=(root) NOPASSWD: /usr/sbin/vgs
%[1]s ALL=(root) NOPASSWD: /usr/sbin/lvs
%[1]s ALL=(root) NOPASSWD: /usr/sbin/dmsetup
%[1]s ALL=(root) NOPASSWD: /usr/bin/truncate
%[1]s ALL=(root) NOPASSWD: /usr/sbin/modprobe
%[1]s ALL=(root) NOPASSWD: /usr/bin/install
`

func createSudoers(ctx context.Context, logger *slog.Logger, runner execx.Runner) error {
	if runtime.GOOS == "darwin" {
		logger.Info("macOS detected - sudoers not needed, podman machine runs rootful")
		return nil
	}

	username := os.Getenv("USER")
	if username == "" {
		return fmt.Errorf("USER environment variable not set")
	}

	binaryName := support.BinaryName
	content := fmt.Sprintf(sudoersTemplate, username, binaryName)
	sudoersPath := fmt.Sprintf("/etc/sudoers.d/%s", binaryName)

	logger.Info("creating sudoers configuration", "path", sudoersPath, "user", username)

	tmpFile := "/tmp/" + binaryName + "-sudoers"
	if err := os.WriteFile(tmpFile, []byte(content), 0440); err != nil {
		return fmt.Errorf("failed to write temp sudoers file: %w", err)
	}
	defer os.Remove(tmpFile)

	result, err := execx.RunSudo(ctx, runner, "visudo", "-c", "-f", tmpFile)
	if err != nil {
		return fmt.Errorf("sudoers validation failed: %w\n%s", err, result.Stderr)
	}

	if _, err := execx.RunSudo(ctx, runner, "install", "-m", "0440", tmpFile, sudoersPath); err != nil {
		return fmt.Errorf("failed to install sudoers file: %w", err)
	}

	logger.Info("sudoers configuration created", "path", sudoersPath)
	logger.Warn("note: these rules grant passwordless sudo for the listed binaries regardless of caller; any process running as your user can invoke them without a password prompt")
	return nil
}

func deleteSudoers(ctx context.Context, logger *slog.Logger, runner execx.Runner) error {
	if runtime.GOOS == "darwin" {
		logger.Info("macOS detected - no sudoers configuration to remove")
		return nil
	}

	binaryName := support.BinaryName
	sudoersPath := fmt.Sprintf("/etc/sudoers.d/%s", binaryName)
	if _, err := os.Stat(sudoersPath); os.IsNotExist(err) {
		logger.Info("sudoers configuration not found", "path", sudoersPath)
		return nil
	}

	logger.Info("removing sudoers configuration", "path", sudoersPath)
	if _, err := execx.RunSudo(ctx, runner, "rm", "-f", sudoersPath); err != nil {
		return fmt.Errorf("failed to remove sudoers file: %w", err)
	}
	logger.Info("sudoers configuration removed")
	return nil
}

func sudoersCommand(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "sudoers",
		Usage: "Manage passwordless sudo configuration for dfmicro (Linux only)",
		UsageText: `Writes /etc/sudoers.d/dfmicro with the commands used by dfmicro requiring elevated access.

No-op on macOS: rootful Podman machine runs as root so no sudoers entry is needed.

Warning: these rules allow any process running as your user to invoke the listed binaries without a password prompt. Intended for developer workstations, not shared hosts.`,
		Action: support.UnknownSubcommand,
		Commands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Write /etc/sudoers.d/dfmicro for the current user",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return createSudoers(ctx, logger, runner)
				},
			},
			{
				Name:      "delete",
				Usage:     "Remove /etc/sudoers.d/dfmicro",
				UsageText: "Removes the sudoers file created by 'sudoers create'. On macOS this is a no-op.",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return deleteSudoers(ctx, logger, runner)
				},
			},
		},
	}
}
