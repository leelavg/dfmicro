package odf

import (
	"context"
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
			"-o", `jsonpath={.status.conditions[?(@.type=="Established")].status}`,
			"--kubeconfig", kubeconfig,
		)
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(result.Stdout) == "True", nil
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
