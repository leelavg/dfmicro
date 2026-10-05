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
	cluster    string
}

func newOdf(logger *slog.Logger, runner execx.Runner, useKubectl bool, kubeconfig, cluster string) *odf {
	kt := "oc"
	if useKubectl {
		kt = "kubectl"
	}
	return &odf{logger: logger, runner: runner, kubectl: kt, kubeconfig: kubeconfig, cluster: cluster}
}
