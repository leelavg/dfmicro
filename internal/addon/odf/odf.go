package odf

import (
	"log/slog"

	"dfmicro/internal/execx"
)

type odf struct {
	logger      *slog.Logger
	runner      execx.Runner
	kubeconfig  string
	clusterName string
}

func newOdf(logger *slog.Logger, runner execx.Runner, kubeconfig, clusterName string) *odf {
	return &odf{logger: logger, runner: runner, kubeconfig: kubeconfig, clusterName: clusterName}
}
