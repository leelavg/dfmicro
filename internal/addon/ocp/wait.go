package ocp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

const (
	resourcePollInterval = 2 * time.Second
	resourceWaitTimeout  = 10 * time.Minute
)

func WaitForCRD(ctx context.Context, runner execx.Runner, kubeClient, kubeconfig, name string) error {
	err := support.Poll(ctx, resourcePollInterval, resourceWaitTimeout, func(ctx context.Context) (bool, error) {
		result, err := runner.Run(ctx, kubeClient,
			"get", "crd/"+name, "--ignore-not-found", "-o", "json",
			"--kubeconfig", kubeconfig,
		)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(result.Stdout) == "" {
			return false, nil
		}

		var crd struct {
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		}
		if err := json.Unmarshal([]byte(result.Stdout), &crd); err != nil {
			return false, fmt.Errorf("decode CRD %s: %w", name, err)
		}
		for _, condition := range crd.Status.Conditions {
			if condition.Type == "Established" {
				return strings.EqualFold(condition.Status, "true"), nil
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("wait for CRD %s establishment: %w", name, err)
	}
	return nil
}

func WaitForCSV(ctx context.Context, runner execx.Runner, kubeClient, kubeconfig, namespace, operator string) (string, error) {
	var csvName string
	err := support.Poll(ctx, resourcePollInterval, resourceWaitTimeout, func(ctx context.Context) (bool, error) {
		result, err := runner.Run(ctx, kubeClient,
			"get", "csv", "-n", namespace,
			"-l", "operators.coreos.com/"+operator+"."+namespace,
			"-o", "jsonpath={.items[0].metadata.name}",
			"--kubeconfig", kubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("get %s CSV: %w", operator, err)
		}
		csvName = strings.TrimSpace(result.Stdout)
		return csvName != "", nil
	})
	if err != nil {
		return "", fmt.Errorf("wait for %s CSV: %w", operator, err)
	}
	return csvName, nil
}
