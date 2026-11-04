package locals3

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"dfmicro/internal/support"
)

func (m *manager) ensureVolume(ctx context.Context) error {
	exists, err := m.volumeExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return m.verifyVolume(ctx)
	}
	if _, err := support.RunPodmanPrivileged(ctx, m.runner,
		"volume", "create", "--label", "created-by=dfmicro", volumeName); err != nil {
		return fmt.Errorf("create local-s3 data volume: %w", err)
	}
	return nil
}

func (m *manager) verifyVolume(ctx context.Context) error {
	result, err := support.RunPodmanPrivileged(ctx, m.runner,
		"volume", "inspect", "--format", `{{index .Labels "created-by"}}`, volumeName)
	if err != nil {
		return fmt.Errorf("inspect local-s3 data volume: %w", err)
	}
	if strings.TrimSpace(result.Stdout) != "dfmicro" {
		return fmt.Errorf("Podman volume %q is not owned by local-s3", volumeName)
	}
	return nil
}

func (m *manager) volumeExists(ctx context.Context) (bool, error) {
	_, err := support.RunPodmanPrivileged(ctx, m.runner, "volume", "exists", volumeName)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("check local-s3 data volume: %w", err)
}
