package network

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"dfmicro/internal/execx"
)

type bridgeConfig struct {
	Name         string
	Subnet       string
	SegmentCount int
}

type bridgeManager struct {
	logger *slog.Logger
	runner execx.Runner
}

func newBridgeManager(logger *slog.Logger, runner execx.Runner) *bridgeManager {
	return &bridgeManager{
		logger: logger,
		runner: runner,
	}
}

func (m *bridgeManager) create(ctx context.Context, cfg bridgeConfig) error {
	exists, err := m.exists(ctx, cfg.Name)
	if err != nil {
		return err
	}
	if exists {
		m.logger.Info("bridge already exists", "name", cfg.Name)
		return nil
	}

	reservedEnd, err := computeReservedIPRange(cfg.Subnet, cfg.SegmentCount)
	if err != nil {
		return fmt.Errorf("failed to compute reserved IP range: %w", err)
	}

	m.logger.Info("creating bridge", "name", cfg.Name, "subnet", cfg.Subnet)
	args := []string{"network", "create", "--opt", "no_default_route=true"}
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

	state := &bridgeState{
		Name:         cfg.Name,
		Subnet:       cfg.Subnet,
		SegmentCount: cfg.SegmentCount,
	}
	if err := state.save(bridgeStateDir()); err != nil {
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

func (m *bridgeManager) getSubnet(ctx context.Context, name string) (string, error) {
	result, err := execx.RunPodmanCommand(ctx, m.runner, "network", "inspect", name, "--format", "{{json .Subnets}}")
	if err != nil {
		return "", fmt.Errorf("failed to inspect bridge: %w", err)
	}
	output := strings.TrimSpace(result.Stdout)
	if output == "" {
		return "", fmt.Errorf("no subnets found for bridge %s", name)
	}

	var subnets []map[string]any
	if err := json.Unmarshal([]byte(output), &subnets); err != nil {
		return "", fmt.Errorf("failed to parse subnets JSON: %w", err)
	}
	if len(subnets) == 0 {
		return "", fmt.Errorf("no subnets found for bridge %s", name)
	}

	subnet, ok := subnets[0]["subnet"].(string)
	if !ok {
		return "", fmt.Errorf("subnet is not a string: %v", subnets[0]["subnet"])
	}
	return subnet, nil
}

func (m *bridgeManager) delete(ctx context.Context, name string) error {
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
