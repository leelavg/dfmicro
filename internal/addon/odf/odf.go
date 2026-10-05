package odf

import (
	"crypto/sha1"
	"fmt"
	"log/slog"

	"dfmicro/internal/execx"
)

type odf struct {
	logger      *slog.Logger
	runner      execx.Runner
	kubectl     string
	kubeconfig  string
	clusterName string
}

func newOdf(logger *slog.Logger, runner execx.Runner, useKubectl bool, kubeconfig, clusterName string) *odf {
	kt := "oc"
	if useKubectl {
		kt = "kubectl"
	}
	return &odf{logger: logger, runner: runner, kubectl: kt, kubeconfig: kubeconfig, clusterName: clusterName}
}

func clusterID(clusterName string) string {
	hash := sha1.Sum([]byte("dfmicro:" + clusterName))
	hash[6] = (hash[6] & 0x0f) | 0x50
	hash[8] = (hash[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		hash[0:4], hash[4:6], hash[6:8], hash[8:10], hash[10:16])
}
