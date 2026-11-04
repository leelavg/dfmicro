package locals3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/support"
)

func (m *manager) uninstall(ctx context.Context, cleanup bool) error {
	clusters, err := loadState()
	if err != nil {
		return err
	}
	for len(clusters) > 0 {
		cluster := clusters[0]
		kubeconfig, err := rootconfig.Kubeconfig(cluster)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				clusters = clusters[1:]
				if err := saveState(clusters); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("read kubeconfig for configured cluster %q: %w", cluster, err)
		}
		m.logger.Info("removing local-s3 Services", "cluster", cluster)
		if _, err := m.runner.Run(ctx, "kubectl", "delete", "services,endpointslices",
			"s3", "-n", "default", "--ignore-not-found", "--kubeconfig", kubeconfig); err != nil {
			return fmt.Errorf("remove local-s3 Services from cluster %q before uninstall: %w", cluster, err)
		}
		clusters = clusters[1:]
		if err := saveState(clusters); err != nil {
			return err
		}
	}

	exists, err := support.ContainerExists(ctx, m.runner, containerName)
	if err != nil {
		return err
	}
	if exists {
		label, err := support.RunPodmanPrivileged(ctx, m.runner, "inspect", "--format", `{{index .Config.Labels "created-by"}}`, containerName)
		if err != nil {
			return err
		}
		if strings.TrimSpace(label.Stdout) != "dfmicro" {
			return fmt.Errorf("container %q is not owned by local-s3", containerName)
		}
		if _, err := support.RunPodmanPrivileged(ctx, m.runner, "rm", "--force", containerName); err != nil {
			return fmt.Errorf("remove local-s3 container: %w", err)
		}
	}
	if cleanup {
		exists, err := m.volumeExists(ctx)
		if err != nil {
			return err
		}
		if exists {
			if err := m.verifyVolume(ctx); err != nil {
				return err
			}
			if _, err := support.RunPodmanPrivileged(ctx, m.runner, "volume", "rm", volumeName); err != nil {
				return fmt.Errorf("remove local-s3 data volume: %w", err)
			}
		}
	}
	return saveState(clusters)
}
