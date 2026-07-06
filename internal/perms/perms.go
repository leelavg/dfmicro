package perms

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
# Created by: %[2]s perms create
%[1]s ALL=(root) NOPASSWD: /usr/bin/podman
%[1]s ALL=(root) NOPASSWD: /usr/sbin/losetup
%[1]s ALL=(root) NOPASSWD: /usr/sbin/vgcreate
%[1]s ALL=(root) NOPASSWD: /usr/sbin/lvcreate
%[1]s ALL=(root) NOPASSWD: /usr/sbin/lvremove
%[1]s ALL=(root) NOPASSWD: /usr/sbin/vgremove
%[1]s ALL=(root) NOPASSWD: /usr/sbin/vgs
%[1]s ALL=(root) NOPASSWD: /usr/bin/truncate
`

func createPerms(ctx context.Context, logger *slog.Logger, runner execx.Runner) error {
	if runtime.GOOS == "darwin" {
		logger.Info("macOS detected - sudo configuration not needed")
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
	logger.Info("you can now run " + binaryName + " commands without password prompts")
	return nil
}

func deletePerms(ctx context.Context, logger *slog.Logger, runner execx.Runner) error {
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

func Command(logger *slog.Logger, runner execx.Runner) *cli.Command {
	return &cli.Command{
		Name:  "perms",
		Usage: "Manage sudo permissions for " + support.BinaryName + " (Linux only)",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return cli.ShowSubcommandHelp(cmd)
		},
		Commands: []*cli.Command{
			{
				Name:  "create",
				Usage: "Create sudoers configuration for passwordless sudo",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return createPerms(ctx, logger, runner)
				},
			},
			{
				Name:  "delete",
				Usage: "Remove sudoers configuration",
				Action: func(ctx context.Context, cmd *cli.Command) error {
					return deletePerms(ctx, logger, runner)
				},
			},
		},
	}
}
