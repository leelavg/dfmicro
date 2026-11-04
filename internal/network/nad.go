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
	master     string
	ipamType   string
}

type nadManager struct {
	logger *slog.Logger
	runner execx.Runner
}

func newNADManager(logger *slog.Logger, runner execx.Runner) *nadManager {
	return &nadManager{
		logger: logger,
		runner: runner,
	}
}

func (m *nadManager) create(ctx context.Context, cfg nadConfig) error {
	m.logger.Info("creating NetworkAttachmentDefinition", "name", cfg.name, "namespace", cfg.namespace)

	nadYAML, err := m.render(cfg)
	if err != nil {
		return err
	}
	return support.ApplyYAML(ctx, m.runner, support.KubeClient, cfg.kubeconfig, nadYAML)
}

func (m *nadManager) delete(ctx context.Context, name, namespace, kubeconfig string) error {
	m.logger.Info("deleting NetworkAttachmentDefinition", "name", name, "namespace", namespace)
	args := []string{"delete", "net-attach-def", name, "-n", namespace, "--ignore-not-found"}
	if kubeconfig != "" {
		args = append(args, "--kubeconfig", kubeconfig)
	}
	_, err := m.runner.Run(ctx, support.KubeClient, args...)
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
		"Master":     cfg.master,
		"IPAMType":   cfg.ipamType,
	}
	return support.Render(nadTemplate, nadCfg)
}
