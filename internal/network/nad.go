package network

import (
	"context"
	"log/slog"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

type nadConfig struct {
	Name       string
	Namespace  string
	Bridge     string
	Subnet     string
	RangeStart string
	RangeEnd   string
}

type nadManager struct {
	logger     *slog.Logger
	runner     execx.Runner
	kubectl    string
	kubeconfig string
}

func newNADManager(logger *slog.Logger, runner execx.Runner, kubectl, kubeconfig string) *nadManager {
	return &nadManager{
		logger:     logger,
		runner:     runner,
		kubectl:    kubectl,
		kubeconfig: kubeconfig,
	}
}

func (m *nadManager) create(ctx context.Context, cfg nadConfig) error {
	m.logger.Info("creating NetworkAttachmentDefinition", "name", cfg.Name, "namespace", cfg.Namespace)

	nadYAML, err := m.render(cfg)
	if err != nil {
		return err
	}
	return support.ApplyYAML(ctx, m.runner, m.kubectl, m.kubeconfig, nadYAML)
}

func (m *nadManager) delete(ctx context.Context, name, namespace string) error {
	m.logger.Info("deleting NetworkAttachmentDefinition", "name", name, "namespace", namespace)
	args := []string{"delete", "net-attach-def", name, "-n", namespace}
	if m.kubeconfig != "" {
		args = append(args, "--kubeconfig", m.kubeconfig)
	}
	_, err := m.runner.Run(ctx, m.kubectl, args...)
	return err
}

func (m *nadManager) render(cfg nadConfig) (string, error) {
	nadCfg := map[string]string{
		"Name":       cfg.Name,
		"Namespace":  cfg.Namespace,
		"Bridge":     cfg.Bridge,
		"Subnet":     cfg.Subnet,
		"RangeStart": cfg.RangeStart,
		"RangeEnd":   cfg.RangeEnd,
	}
	return support.Render(nadTemplate, nadCfg)
}
