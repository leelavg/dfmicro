package odf

import (
	"log/slog"

	"dfmicro/internal/execx"
)

type odf struct {
	logger     *slog.Logger
	runner     execx.Runner
	kubectl    string
	kubeconfig string
}

func newOdf(logger *slog.Logger, runner execx.Runner, useKubectl bool, kubeconfig string) *odf {
	kt := "oc"
	if useKubectl {
		kt = "kubectl"
	}
	return &odf{logger: logger, runner: runner, kubectl: kt, kubeconfig: kubeconfig}
}
