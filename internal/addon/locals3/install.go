package locals3

import (
	"context"
	"fmt"
	"strings"
	"time"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

func (m *manager) install(ctx context.Context, image string) error {
	exists, err := support.ContainerExists(ctx, m.runner, containerName)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("container %q already exists", containerName)
	}

	if err := m.ensureVolume(ctx); err != nil {
		return err
	}
	network := rootconfig.Load().BridgeName
	args := []string{
		"run", "--detach", "--name", containerName,
		"--network", network,
		"--label", "created-by=dfmicro",
		"--volume", volumeName + ":/data",
		"--env", "S3_DATA_DIR=/data",
		image,
	}
	m.logger.Info("starting local S3 service", "image", image, "volume", volumeName)
	if _, err := support.RunPodmanPrivileged(ctx, m.runner, args...); err != nil {
		return fmt.Errorf("start local-s3 container: %w", err)
	}
	m.logger.Info("waiting for local S3 container", "container", containerName)
	var address string
	err = support.Poll(ctx, time.Second, time.Minute, func(ctx context.Context) (bool, error) {
		format := fmt.Sprintf(`{{.State.Status}} {{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{end}}`, network)
		result, err := support.RunPodmanPrivileged(ctx, m.runner, "inspect", "--format", format, containerName)
		if err != nil {
			return false, err
		}
		state := strings.Fields(result.Stdout)
		if len(state) != 2 || state[0] != "running" || state[1] == "" {
			return false, nil
		}
		address = state[1]
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("wait for local-s3 container: %w", err)
	}
	m.logger.Info("local S3 service started", "endpoint", fmt.Sprintf("http://%s:9000", address), "access", "anonymous")
	return nil
}
