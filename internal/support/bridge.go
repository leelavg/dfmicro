package support

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"dfmicro/internal/execx"
)

type BridgeConfig struct {
	Name                   string
	Subnet                 string
	SegmentCount           int
	ReservePerSegmentCount int
	StateDir               string
	NoDefaultRoute         bool
}

type bridgeManager struct {
	logger *slog.Logger
	runner execx.Runner
}

func NewBridgeManager(logger *slog.Logger, runner execx.Runner) *bridgeManager {
	return &bridgeManager{
		logger: logger,
		runner: runner,
	}
}

func (m *bridgeManager) Create(ctx context.Context, cfg BridgeConfig) error {
	exists, err := m.exists(ctx, cfg.Name)
	if err != nil {
		return err
	}
	if exists {
		m.logger.Info("bridge already exists", "name", cfg.Name)
		return nil
	}

	reservedEnd, err := ComputeReservedIPRange(cfg.Subnet, cfg.SegmentCount, cfg.ReservePerSegmentCount)
	if err != nil {
		return fmt.Errorf("failed to compute reserved IP range: %w", err)
	}

	m.logger.Info("creating bridge", "name", cfg.Name, "subnet", cfg.Subnet)
	args := []string{"network", "create"}
	if cfg.NoDefaultRoute {
		args = append(args, "--opt", "no_default_route=true")
	}
	if cfg.Subnet != "" {
		args = append(args, "--subnet", cfg.Subnet)
	}
	if reservedEnd != "" {
		args = append(args, "--ip-range", reservedEnd)
	}
	args = append(args, cfg.Name)
	_, err = execx.RunPodmanCommand(ctx, m.runner, args...)
	if err != nil {
		return fmt.Errorf("failed to create bridge: %w", err)
	}

	state := &BridgeState{
		Name:         cfg.Name,
		Subnet:       cfg.Subnet,
		SegmentCount: cfg.SegmentCount,
	}
	if err := state.Save(cfg.StateDir); err != nil {
		return fmt.Errorf("failed to save bridge state: %w", err)
	}

	m.logger.Info("bridge created and state saved", "name", state.Name, "segmentCount", state.SegmentCount)
	return nil
}

func (m *bridgeManager) exists(ctx context.Context, name string) (bool, error) {
	_, err := execx.RunPodmanCommand(ctx, m.runner, "network", "exists", name)
	if err == nil {
		return true, nil
	}
	return false, nil
}

func (m *bridgeManager) Delete(ctx context.Context, name, stateDir string) error {

	ipamPath := filepath.Join(stateDir, fmt.Sprintf("ipam-%s.json", name))
	if err := os.Remove(ipamPath); err != nil && !os.IsNotExist(err) {
		m.logger.Warn("failed to delete IPAM state file", "path", ipamPath, "error", err)
	}

	bridgeStatePath := filepath.Join(stateDir, fmt.Sprintf("bridge-%s.json", name))
	if err := os.Remove(bridgeStatePath); err != nil && !os.IsNotExist(err) {
		m.logger.Warn("failed to delete bridge state file", "path", bridgeStatePath, "error", err)
	}

	exists, err := m.exists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		m.logger.Info("bridge does not exist", "name", name)
		return nil
	}

	m.logger.Info("deleting bridge", "name", name)
	_, err = execx.RunPodmanCommand(ctx, m.runner, "network", "rm", name)
	if err != nil {
		return fmt.Errorf("failed to delete bridge: %w", err)
	}
	return nil
}

type BridgeState struct {
	Name              string `json:"name"`
	Subnet            string `json:"subnet"`
	SegmentCount      int    `json:"segmentCount"`
	ReservePerSegment int    `json:"reservePerSegment"`
}

func LoadBridgeState(stateDir, name string) (*BridgeState, error) {
	path := filepath.Join(stateDir, fmt.Sprintf("bridge-%s.json", name))
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("bridge state not found for %s", name)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state BridgeState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (b *BridgeState) Save(stateDir string) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(stateDir, fmt.Sprintf("bridge-%s.json", b.Name))
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func NetworkStateDir(baseDir string) string {
	return filepath.Join(baseDir, ",networks")
}
