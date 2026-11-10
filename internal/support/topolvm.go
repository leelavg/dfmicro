package support

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/template"
	"time"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
)

//go:embed topolvm-assets/*
var topolvmAssets embed.FS

var errNoLoopDevice = errors.New("no loop device associated")

type topoLVMConfig struct {
	enabled            bool
	volumeSize         string
	overprovisionRatio float32
	thinpool           bool
}

type TopoLVMMgr struct {
	logger   *slog.Logger
	runner   execx.Runner
	stateDir string
	config   topoLVMConfig
}

func NewTopoLVMMgr(logger *slog.Logger, runner execx.Runner, cfg rootconfig.ClusterConfig) *TopoLVMMgr {
	return &TopoLVMMgr{logger: logger, runner: runner, stateDir: cfg.StateDir, config: topoLVMConfig{
		enabled:            cfg.EnableTopoLVM,
		volumeSize:         cfg.LVMVolSize,
		overprovisionRatio: cfg.OverprovisionRatio,
		thinpool:           cfg.EnableThinpool,
	}}
}

func (t *TopoLVMMgr) Enabled() bool {
	return t.config.enabled
}

func (t *TopoLVMMgr) ManifestDir() string {
	return filepath.Join(t.stateDir, "topolvm")
}

func (t *TopoLVMMgr) RemoveManifest() error {
	if !t.Enabled() {
		return nil
	}
	return os.RemoveAll(t.ManifestDir())
}

func (t *TopoLVMMgr) Render(ctx context.Context, clusterName string, extraNodes ...string) error {
	if !t.Enabled() {
		return nil
	}
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

func (t *TopoLVMMgr) Reconcile(ctx context.Context, clusterName, container string) error {
	if !t.Enabled() {
		return nil
	}
	if err := t.Render(ctx, clusterName); err != nil {
		return err
	}
	return ApplyKustomization(ctx, t.runner, container, "/usr/lib/microshift/manifests.d/001-microshift-topolvm")
}

func (t *TopoLVMMgr) CreateBackend(ctx context.Context, nodeName string) error {
	if !t.Enabled() {
		return nil
	}
	disk := filepath.Join(t.stateDir, nodeName, nodeName+".image")
	return t.createBackend(ctx, disk, nodeName)
}

func (t *TopoLVMMgr) LVMConfigMount(ctx context.Context, nodeName string) (string, error) {
	if !t.Enabled() {
		return "", nil
	}
	config, err := t.lvmConfig(ctx, nodeName)
	if err != nil {
		return "", err
	}
	path := filepath.Join(t.stateDir, nodeName, "lvm.conf")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(config), 0o644); err != nil {
		return "", err
	}
	return path + ":/etc/lvm/lvm.conf:ro", nil
}

func (t *TopoLVMMgr) lvmConfig(ctx context.Context, nodeName string) (string, error) {
	device, err := t.loopDevice(ctx, filepath.Join(t.stateDir, nodeName, nodeName+".image"))
	if err != nil {
		return "", err
	}
	if device == "" {
		return "", fmt.Errorf("%w with %s", errNoLoopDevice, nodeName)
	}
	// Disable the devices file so LVM honors this exact-device filter.
	return fmt.Sprintf(`devices {
    use_devicesfile = 0
    filter = [ "a|^%s$|", "r|.*|" ]
    global_filter = [ "a|^%s$|", "r|.*|" ]
}`, device, device), nil
}

func (t *TopoLVMMgr) loopDevice(ctx context.Context, disk string) (string, error) {
	if _, err := os.Stat(disk); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	result, err := RunPrivileged(ctx, t.runner, "losetup", "--associated", disk, "--output", "NAME", "--noheadings")
	if err != nil {
		return "", err
	}
	devices := strings.Fields(result.Stdout)
	if len(devices) == 0 {
		return "", nil
	}
	if len(devices) != 1 {
		return "", fmt.Errorf("expected one loop device for %s, found %d", disk, len(devices))
	}
	device := devices[0]
	number := strings.TrimPrefix(device, "/dev/loop")
	if number == device || number == "" {
		return "", fmt.Errorf("unexpected loop device %q for %s", device, disk)
	}
	for _, digit := range number {
		if digit < '0' || digit > '9' {
			return "", fmt.Errorf("unexpected loop device %q for %s", device, disk)
		}
	}
	return device, nil
}

func (t *TopoLVMMgr) runLVM(ctx context.Context, nodeName, command string, args ...string) (execx.Result, error) {
	config, err := t.lvmConfig(ctx, nodeName)
	if err != nil {
		return execx.Result{}, err
	}
	lvmArgs := append([]string{"--config", config}, args...)
	return RunPrivileged(ctx, t.runner, command, lvmArgs...)
}

func (t *TopoLVMMgr) DeleteBackends(ctx context.Context, nodeNames ...string) error {
	if !t.Enabled() {
		return nil
	}
	var errs []error
	for _, name := range nodeNames {
		disk := filepath.Join(t.stateDir, name, name+".image")
		if err := t.deleteBackend(ctx, disk, name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (t *TopoLVMMgr) DeleteNodeResources(ctx context.Context, controlNode, nodeName string) error {
	if !t.Enabled() {
		return nil
	}
	_, err := RunPodmanPrivileged(ctx, t.runner, "exec", controlNode, "oc", "-n", "topolvm-system", "delete", "daemonset,configmap", "-l", "dfmicro.io/topolvm-node="+nodeName, "--ignore-not-found")
	return err
}

func (t *TopoLVMMgr) writeManifest(nodes []string) error {
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

func (t *TopoLVMMgr) writeAssets(dir string) error {
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

func (t *TopoLVMMgr) renderManifest(nodes []string) ([]byte, error) {
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
			OverprovisionRatio: t.config.overprovisionRatio,
			Thinpool:           t.config.thinpool,
		}
		if err := tmpl.Execute(&rendered, data); err != nil {
			return nil, err
		}
	}
	return []byte(rendered.String()), nil
}

func (t *TopoLVMMgr) createBackend(ctx context.Context, disk, vg string) error {
	imageExists := false
	if _, err := os.Stat(disk); err == nil {
		imageExists = true
		device, err := t.loopDevice(ctx, disk)
		if err != nil {
			return err
		}
		var result execx.Result
		if device != "" {
			result, err = t.runLVM(ctx, vg, "vgs", "--noheadings", "-o", "vg_name", vg)
		}
		if device != "" && err == nil && strings.TrimSpace(result.Stdout) == vg {
			if !t.config.thinpool {
				return nil
			}
			result, err := t.runLVM(ctx, vg, "lvs", "--noheadings", "-o", "lv_name", vg)
			if err == nil {
				for lv := range strings.FieldsSeq(result.Stdout) {
					if lv == "thin" {
						return nil
					}
				}
			}
			if err := t.deleteBackend(ctx, disk, vg); err != nil {
				return fmt.Errorf("remove incomplete volume group %s: %w", vg, err)
			}
			imageExists = false
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
		return err
	}
	if !imageExists {
		if _, err := RunPrivileged(ctx, t.runner, "truncate", "--size="+t.config.volumeSize, disk); err != nil {
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
	if _, err := t.runLVM(ctx, vg, "vgcreate", "-f", "-y", vg, device); err != nil {
		_, _ = RunPrivileged(ctx, t.runner, "losetup", "--detach", device)
		return err
	}
	if t.config.thinpool {
		if err := t.waitForVolumeGroup(ctx, vg); err != nil {
			if cleanupErr := t.deleteBackend(ctx, disk, vg); cleanupErr != nil {
				return fmt.Errorf("%w (cleanup failed: %v)", err, cleanupErr)
			}
			return err
		}
		err := Retry(ctx, 60, time.Second, isTransientThinpoolError, func(ctx context.Context) error {
			if _, err := RunUnprivileged(ctx, t.runner, "udevadm", "settle"); err != nil {
				return err
			}
			_, err := t.runLVM(ctx, vg, "lvcreate", "--zero", "n", "-l", "99%FREE", "--thinpool", "thin", vg)
			return err
		})
		if err == nil {
			return nil
		}
		if cleanupErr := t.deleteBackend(ctx, disk, vg); cleanupErr != nil {
			return fmt.Errorf("%w (cleanup failed: %v)", err, cleanupErr)
		}
		return err
	}
	return nil
}

func (t *TopoLVMMgr) waitForVolumeGroup(ctx context.Context, vg string) error {
	err := Poll(ctx, time.Second, time.Minute, func(ctx context.Context) (bool, error) {
		result, err := t.runLVM(ctx, vg, "vgs", "--noheadings", "-o", "vg_name", vg)
		return err == nil && strings.TrimSpace(result.Stdout) == vg, nil
	})
	if err != nil {
		return fmt.Errorf("wait for volume group %s: %w", vg, err)
	}
	return nil
}

func isTransientThinpoolError(err error) bool {
	message := err.Error()
	for _, text := range []string{
		"device not cleared",
		"Failed to activate new LV",
		"thin_tmeta",
		"thin_tdata",
		"metadata spare LV",
	} {
		if strings.Contains(message, text) {
			return true
		}
	}
	return false
}

func (t *TopoLVMMgr) deleteBackend(ctx context.Context, disk, vg string) error {
	device, err := t.loopDevice(ctx, disk)
	if err != nil {
		return err
	}
	runRemove := func(command string) error {
		_, err := t.runLVM(ctx, vg, command, "--force", "-y", vg)
		if errors.Is(err, errNoLoopDevice) {
			t.logger.Warn("no loop device associated, skipping LVM removal", "node", vg)
			device = ""
			return nil
		}
		return err
	}
	if device == "" {
		t.logger.Warn("no loop device associated, skipping LVM removal", "node", vg)
	}
	if device != "" {
		if err := runRemove("lvremove"); err != nil && !isMissingLVMResource(err) {
			if cleanupErr := t.removeDeviceMappings(ctx, vg); cleanupErr != nil {
				return fmt.Errorf("remove logical volumes for %s: %w (device cleanup failed: %v)", vg, err, cleanupErr)
			}
			_, _ = RunUnprivileged(ctx, t.runner, "udevadm", "settle")
			if retryErr := runRemove("lvremove"); retryErr != nil && !isMissingLVMResource(retryErr) {
				return fmt.Errorf("remove logical volumes for %s: %w", vg, retryErr)
			}
		}
	}
	if err := t.removeDeviceMappings(ctx, vg); err != nil {
		return fmt.Errorf("remove device mappings for %s: %w", vg, err)
	}
	_, _ = RunUnprivileged(ctx, t.runner, "udevadm", "settle")
	if device != "" {
		if err := runRemove("vgremove"); err != nil && !isMissingLVMResource(err) {
			return fmt.Errorf("remove volume group %s: %w", vg, err)
		}
	}

	result, err := RunPrivileged(ctx, t.runner, "losetup", "--associated", disk, "--output", "NAME", "--noheadings")
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

func (t *TopoLVMMgr) removeDeviceMappings(ctx context.Context, vg string) error {
	result, err := RunPrivileged(ctx, t.runner, "dmsetup", "ls", "--noheadings", "-C", "-o", "name")
	if err != nil {
		if isMissingLVMResource(err) {
			return nil
		}
		return err
	}

	var devices []string
	for line := range strings.SplitSeq(strings.TrimSpace(result.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && deviceVolumeGroup(fields[0]) == vg {
			devices = append(devices, fields[0])
		}
	}
	if len(devices) == 0 {
		return nil
	}

	sort.SliceStable(devices, func(i, j int) bool {
		return mappingDepth(devices[i]) < mappingDepth(devices[j])
	})
	for _, device := range devices {
		err := Retry(ctx, 10, 200*time.Millisecond, func(err error) bool {
			return !isMissingLVMResource(err)
		}, func(ctx context.Context) error {
			_, err := RunPrivileged(ctx, t.runner, "dmsetup", "remove", "--force", device)
			if err != nil && !isMissingLVMResource(err) {
				_, _ = RunUnprivileged(ctx, t.runner, "udevadm", "settle")
			}
			return err
		})
		if err != nil {
			return fmt.Errorf("remove device mapping %s: %w", device, err)
		}
	}
	return nil
}

func deviceVolumeGroup(name string) string {
	var decoded strings.Builder
	for index := 0; index < len(name); index++ {
		if name[index] != '-' {
			decoded.WriteByte(name[index])
			continue
		}
		if index+1 < len(name) && name[index+1] == '-' {
			decoded.WriteByte('-')
			index++
			continue
		}
		return decoded.String()
	}
	return ""
}

func mappingDepth(name string) int {
	switch {
	case strings.HasSuffix(name, "-tpool"):
		return 1
	case strings.HasSuffix(name, "_tdata"), strings.HasSuffix(name, "_tmeta"):
		return 2
	default:
		return 0
	}
}

func isMissingLVMResource(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "not found") || strings.Contains(text, "does not exist") || strings.Contains(text, "no such device")
}
