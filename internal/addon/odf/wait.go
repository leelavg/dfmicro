package odf

import (
	"context"
	"fmt"
	"strings"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

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
