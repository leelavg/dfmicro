package odr

import (
	"log/slog"

	"dfmicro/internal/execx"
)

type odr struct {
	logger      *slog.Logger
	runner      execx.Runner
	clusterName string
	kubeconfig  string
}
