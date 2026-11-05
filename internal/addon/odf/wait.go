package odf

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

func waitForCRD(ctx context.Context, runner execx.Runner, kubeClient, kubeconfig, name string) error {
	if _, err := runner.Run(ctx, kubeClient,
		"wait", "--for=create", "--timeout=10m", "crd/"+name,
		"--kubeconfig", kubeconfig,
	); err != nil {
		return fmt.Errorf("wait for CRD %s creation: %w", name, err)
	}
	err := support.Poll(ctx, odfPoll, odfTimeout, func(ctx context.Context) (bool, error) {
		result, err := runner.Run(ctx, kubeClient,
			"get", "crd/"+name,
			"-o", "json",
			"--kubeconfig", kubeconfig,
		)
		if err != nil {
			return false, err
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

func waitForSub(ctx context.Context, runner execx.Runner, kubeClient, kubeconfig, output string, ready func(string) bool) (string, error) {
	var subscription string
	err := support.Poll(ctx, odfPoll, odfTimeout, func(ctx context.Context) (bool, error) {
		result, err := runner.Run(ctx, kubeClient,
			"get", "subscription", "-n", providerNamespace,
			"-o", output,
			"--kubeconfig", kubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("get subscription: %w", err)
		}
		subscription = strings.TrimSpace(result.Stdout)
		return ready(subscription), nil
	})
	if err != nil {
		return "", err
	}
	return subscription, nil
}

func waitForCSV(ctx context.Context, runner execx.Runner, kubeClient, kubeconfig, name, output string) (string, error) {
	var csvName string
	err := support.Poll(ctx, odfPoll, odfTimeout, func(ctx context.Context) (bool, error) {
		result, err := runner.Run(ctx, kubeClient,
			"get", "csv", "-n", providerNamespace,
			"-o", output,
			"--kubeconfig", kubeconfig,
		)
		if err != nil {
			return false, fmt.Errorf("failed to find %s CSV: %w", name, err)
		}
		csvName = strings.TrimSpace(result.Stdout)
		return csvName != "", nil
	})
	if err != nil {
		return "", err
	}
	return csvName, nil
}
