package network

import (
	"context"
	"log/slog"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

type nadConfig struct {
	name       string
	namespace  string
	kubeconfig string
	bridge     string
	subnet     string
	rangeStart string
	rangeEnd   string
}

type nadManager struct {
	logger  *slog.Logger
	runner  execx.Runner
	kubectl string
}

func newNADManager(logger *slog.Logger, runner execx.Runner, kubectl string) *nadManager {
	return &nadManager{
		logger:  logger,
		runner:  runner,
		kubectl: kubectl,
	}
}

func (m *nadManager) create(ctx context.Context, cfg nadConfig) error {
	m.logger.Info("creating NetworkAttachmentDefinition", "name", cfg.name, "namespace", cfg.namespace)

	nadYAML, err := m.render(cfg)
	if err != nil {
		return err
	}
	return support.ApplyYAML(ctx, m.runner, m.kubectl, cfg.kubeconfig, nadYAML)
}

func (m *nadManager) delete(ctx context.Context, name, namespace, kubeconfig string) error {
	m.logger.Info("deleting NetworkAttachmentDefinition", "name", name, "namespace", namespace)
	args := []string{"delete", "net-attach-def", name, "-n", namespace}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	_, err := m.runner.Run(ctx, m.kubectl, args...)
	return err
}

func (m *nadManager) render(cfg nadConfig) (string, error) {
	nadCfg := map[string]string{
		"Name":       cfg.name,
		"Namespace":  cfg.namespace,
		"Bridge":     cfg.bridge,
		"Subnet":     cfg.subnet,
		"RangeStart": cfg.rangeStart,
		"RangeEnd":   cfg.rangeEnd,
	}
	return support.Render(nadTemplate, nadCfg)
}
