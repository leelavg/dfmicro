package support

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"dfmicro/internal/execx"
)

//go:embed topolvm-assets/*
var topolvmAssets embed.FS

type TopoLVMConfig struct {
	VolumeSize         string
	OverprovisionRatio float32
	Thinpool           bool
}

type TopoLVM struct {
	runner   execx.Runner
	stateDir string
	config   TopoLVMConfig
}

func NewTopoLVM(runner execx.Runner, stateDir string, config TopoLVMConfig) *TopoLVM {
	return &TopoLVM{runner: runner, stateDir: stateDir, config: config}
}

func (t *TopoLVM) ManifestDir() string {
	return filepath.Join(t.stateDir, "topolvm")
}

func (t *TopoLVM) RemoveManifest() error {
	return os.RemoveAll(t.ManifestDir())
}

func (t *TopoLVM) Render(ctx context.Context, clusterName string, extraNodes ...string) error {
	nodes, err := AllClusterContainers(ctx, t.runner, clusterName)
	if err != nil {
		return err
	}
	for _, extra := range extraNodes {
		if !slices.Contains(nodes, extra) {
			nodes = append(nodes, extra)
		}
	}
	if err := t.writeManifest(nodes); err != nil {
		return err
	}
	return nil
}

func (t *TopoLVM) Apply(ctx context.Context, container string) error {
	return ApplyKustomization(ctx, t.runner, container, "/usr/lib/microshift/manifests.d/001-microshift-topolvm")
}

func (t *TopoLVM) Reconcile(ctx context.Context, clusterName, container string, extraNodes ...string) error {
	if err := t.Render(ctx, clusterName, extraNodes...); err != nil {
		return err
	}
	return t.Apply(ctx, container)
}

func (t *TopoLVM) CreateBackend(ctx context.Context, nodeName string) error {
	disk := filepath.Join(t.stateDir, nodeName, nodeName+".image")
	return t.createBackend(ctx, disk, nodeName)
}

func (t *TopoLVM) DeleteBackends(ctx context.Context, nodeNames ...string) error {
	for _, name := range nodeNames {
		disk := filepath.Join(t.stateDir, name, name+".image")
		if err := t.deleteBackend(ctx, disk, name); err != nil {
			return err
		}
	}
	return nil
}

func (t *TopoLVM) writeManifest(nodes []string) error {
	dir := t.ManifestDir()
	if err := t.writeAssets(dir); err != nil {
		return err
	}

	manifest, err := t.renderManifest(nodes)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "03-dynamic.yaml"), manifest, 0o644)
}

func (t *TopoLVM) writeAssets(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"01-namespace.yaml", "02-topolvm.yaml", "kustomization.yaml"} {
		data, err := topolvmAssets.ReadFile("topolvm-assets/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (t *TopoLVM) renderManifest(nodes []string) ([]byte, error) {
	if len(nodes) == 0 {
		return nil, fmt.Errorf("at least one node is required")
	}
	tmpl, err := template.New("topolvm-lvmd").Parse(topolvmLVMDTemplate)
	if err != nil {
		return nil, err
	}

	var rendered strings.Builder
	for _, node := range nodes {
		data := struct {
			NodeName           string
			VGName             string
			OverprovisionRatio float32
			Thinpool           bool
		}{
			NodeName:           node,
			VGName:             node,
			OverprovisionRatio: t.config.OverprovisionRatio,
			Thinpool:           t.config.Thinpool,
		}
		if err := tmpl.Execute(&rendered, data); err != nil {
			return nil, err
		}
	}
	return []byte(rendered.String()), nil
}

func (t *TopoLVM) createBackend(ctx context.Context, disk, vg string) error {
	imageExists := false
	if _, err := os.Stat(disk); err == nil {
		imageExists = true
		result, err := RunPrivileged(ctx, t.runner, "vgs", "--noheadings", "-o", "vg_name", vg)
		if err == nil && strings.TrimSpace(result.Stdout) == vg {
			result, err := RunPrivileged(ctx, t.runner, "lvs", "--noheadings", "-o", "lv_name", vg)
			if err == nil {
				for lv := range strings.FieldsSeq(result.Stdout) {
					if !t.config.Thinpool || lv == "thin" {
						return nil
					}
				}
			}
			if err := t.deleteBackend(ctx, disk, vg); err != nil {
				return fmt.Errorf("remove incomplete volume group %s: %w", vg, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
		return err
	}
	if !imageExists {
		if _, err := RunPrivileged(ctx, t.runner, "truncate", "--size="+t.config.VolumeSize, disk); err != nil {
			return err
		}
	}

	result, err := RunPrivileged(ctx, t.runner, "losetup", "--find", "--show", "--nooverlap", disk)
	if err != nil {
		return err
	}
	device := strings.TrimSpace(result.Stdout)
	if device == "" {
		return errors.New("losetup did not return a device name")
	}
	if _, err := RunPrivileged(ctx, t.runner, "vgcreate", "-f", "-y", vg, device); err != nil {
		_, _ = RunPrivileged(ctx, t.runner, "losetup", "--detach", device)
		return err
	}
	if t.config.Thinpool {
		if _, err := RunPrivileged(ctx, t.runner, "lvcreate", "--zero", "n", "-l", "99%FREE", "--thinpool", "thin", vg); err != nil {
			if cleanupErr := t.deleteBackend(ctx, disk, vg); cleanupErr != nil {
				return fmt.Errorf("%w (cleanup failed: %v)", err, cleanupErr)
			}
			return err
		}
	}
	return nil
}

func (t *TopoLVM) deleteBackend(ctx context.Context, disk, vg string) error {
	encodedVG := strings.ReplaceAll(vg, "-", "--")
	result, err := RunPrivileged(ctx, t.runner, "dmsetup", "ls", "--noheadings", "-C", "-o", "name")
	if err != nil && !isMissingLVMResource(err) {
		return fmt.Errorf("list device mappings for %s: %w", vg, err)
	}
	if err == nil {
		var devices []string
		for name := range strings.SplitSeq(strings.TrimSpace(result.Stdout), "\n") {
			fields := strings.Fields(name)
			if len(fields) == 0 {
				continue
			}
			name = fields[0]
			if strings.HasPrefix(name, encodedVG+"-") {
				devices = append(devices, name)
			}
		}
		if len(devices) > 0 {
			args := append([]string{"remove", "--force", "--deferred"}, devices...)
			if _, err := RunPrivileged(ctx, t.runner, "dmsetup", args...); err != nil {
				return fmt.Errorf("remove device mappings for %s: %w", vg, err)
			}
		}
	}

	if _, err := RunPrivileged(ctx, t.runner, "lvremove", "--force", "-y", vg); err != nil && !isMissingLVMResource(err) {
		return fmt.Errorf("remove logical volumes for %s: %w", vg, err)
	}
	if _, err := RunPrivileged(ctx, t.runner, "vgremove", "--force", "-y", vg); err != nil && !isMissingLVMResource(err) {
		return fmt.Errorf("remove volume group %s: %w", vg, err)
	}

	result, err = RunPrivileged(ctx, t.runner, "losetup", "--associated", disk, "--output", "NAME", "--noheadings")
	if err == nil {
		for device := range strings.SplitSeq(strings.TrimSpace(result.Stdout), "\n") {
			device = strings.TrimSpace(device)
			if device == "" {
				continue
			}
			if _, err := RunPrivileged(ctx, t.runner, "losetup", "--detach", device); err != nil {
				return fmt.Errorf("detach loop device %s: %w", device, err)
			}
		}
	}

	if err := os.Remove(disk); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func isMissingLVMResource(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not found") || strings.Contains(text, "does not exist")
}
