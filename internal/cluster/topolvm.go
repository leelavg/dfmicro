package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

func (m *manager) createTopoLVMBackend(ctx context.Context) error {
	return createTopoLVMBackend(ctx, m.runner, m.cfg.LVMDisk, m.cfg.VGName, m.cfg.LVMVolSize)
}

func (m *manager) deleteTopoLVMBackend(ctx context.Context) error {
	return deleteTopoLVMBackend(ctx, m.runner, m.cfg.LVMDisk, m.cfg.VGName)
}

func (m *manager) deleteTopoLVMNodeBackends(ctx context.Context) error {
	entries, err := os.ReadDir(filepath.Dir(m.cfg.StateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	prefix := m.cfg.Name + "-"
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) {
			continue
		}
		if _, err := strconv.Atoi(strings.TrimPrefix(entry.Name(), prefix)); err != nil {
			continue
		}
		node := entry.Name()
		disk := filepath.Join(filepath.Dir(m.cfg.StateDir), node, node+".image")
		if err := deleteTopoLVMBackend(ctx, m.runner, disk, node); err != nil {
			return err
		}
	}
	return nil
}

func CreateNodeTopoLVMBackend(ctx context.Context, runner execx.Runner, disk, vg, size string) error {
	return createTopoLVMBackend(ctx, runner, disk, vg, size)
}

func DeleteNodeTopoLVMBackend(ctx context.Context, runner execx.Runner, disk, vg string) error {
	return deleteTopoLVMBackend(ctx, runner, disk, vg)
}

func createTopoLVMBackend(ctx context.Context, runner execx.Runner, disk, vg, size string) error {
	imageExists := false
	if _, err := os.Stat(disk); err == nil {
		imageExists = true
		result, err := support.RunPrivileged(ctx, runner, "vgs", "--noheadings", "-o", "vg_name", vg)
		if err == nil && strings.TrimSpace(result.Stdout) == vg {
			return nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
		return err
	}
	if !imageExists {
		if _, err := support.RunPrivileged(ctx, runner, "truncate", "--size="+size, disk); err != nil {
			return err
		}
	}

	result, err := support.RunPrivileged(ctx, runner, "losetup", "--find", "--show", "--nooverlap", disk)
	if err != nil {
		return err
	}
	device := strings.TrimSpace(result.Stdout)
	if device == "" {
		return errors.New("losetup did not return a device name")
	}
	if _, err := support.RunPrivileged(ctx, runner, "vgcreate", "-f", "-y", vg, device); err != nil {
		return err
	}
	_, err = support.RunPrivileged(ctx, runner, "lvcreate", "--zero", "n", "-l", "99%FREE", "--thinpool", "thin", vg)
	return err
}

func deleteTopoLVMBackend(ctx context.Context, runner execx.Runner, disk, vg string) error {
	encodedVG := strings.ReplaceAll(vg, "-", "--")
	for range 10 {
		result, err := support.RunPrivileged(ctx, runner, "dmsetup", "ls", "--noheadings", "-C", "-o", "name")
		if err != nil {
			break
		}
		var devices []string
		for name := range strings.SplitSeq(strings.TrimSpace(result.Stdout), "\n") {
			fields := strings.Fields(name)
			if len(fields) == 0 {
				continue
			}
			name = fields[0]
			if name == "" || !strings.HasPrefix(name, encodedVG+"-") {
				continue
			}
			devices = append(devices, name)
		}
		if len(devices) == 0 {
			break
		}
		for i := len(devices) - 1; i >= 0; i-- {
			_, _ = support.RunPrivileged(ctx, runner, "dmsetup", "remove", "--force", "--retry", devices[i])
		}
	}

	if _, err := support.RunPrivileged(ctx, runner, "lvremove", "--force", "-y", vg); err != nil {
		if !isMissingLVMResource(err) {
			return fmt.Errorf("remove logical volumes for %s: %w", vg, err)
		}
	}
	if _, err := support.RunPrivileged(ctx, runner, "vgremove", "--force", "-y", vg); err != nil {
		if !isMissingLVMResource(err) {
			return fmt.Errorf("remove volume group %s: %w", vg, err)
		}
	}

	result, err := support.RunPrivileged(ctx, runner, "losetup", "--associated", disk, "--output", "NAME", "--noheadings")
	if err == nil {
		for device := range strings.SplitSeq(strings.TrimSpace(result.Stdout), "\n") {
			device = strings.TrimSpace(device)
			if device != "" {
				_, _ = support.RunPrivileged(ctx, runner, "losetup", "--detach", device)
			}
		}
	}

	return os.RemoveAll(filepath.Dir(disk))
}

func isMissingLVMResource(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not found") || strings.Contains(text, "does not exist")
}
