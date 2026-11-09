package ocm

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"

	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

//go:embed assets/gpf-hub-api-egress.yaml.tmpl
var gpfHubAPIEgressTmpl string

const gpfHubAPIEgressName = "dfmicro-gpf-host-api-egress"

func (o *ocm) allowGPFHubAPI(ctx context.Context, hubKubeconfig, spoke, apiServer string) error {
	endpoint, err := url.Parse(apiServer)
	if err != nil {
		return fmt.Errorf("parse hub API endpoint: %w", err)
	}
	if endpoint.Scheme != "https" {
		return fmt.Errorf("hub API endpoint %q is not HTTPS", apiServer)
	}
	ip := net.ParseIP(endpoint.Hostname())
	if ip == nil || ip.To4() == nil {
		return fmt.Errorf("hub API endpoint %q does not use an IPv4 address", apiServer)
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("hub API endpoint %q has an invalid port", apiServer)
	}

	spokeName, err := json.Marshal(spoke)
	if err != nil {
		return err
	}
	manifest, err := support.Render(gpfHubAPIEgressTmpl, map[string]string{
		"Name":    gpfHubAPIEgressName,
		"Spoke":   string(spokeName),
		"HubIP":   ip.To4().String(),
		"HubPort": strconv.Itoa(port),
	})
	if err != nil {
		return fmt.Errorf("render GPF hub API egress ManifestWork: %w", err)
	}
	if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, hubKubeconfig, manifest); err != nil {
		return fmt.Errorf("apply GPF hub API egress ManifestWork: %w", err)
	}
	if _, err := execx.Run(ctx, o.runner, support.KubeClient,
		"--kubeconfig", hubKubeconfig,
		"wait", "--for=condition=Applied", "--timeout=2m",
		"manifestwork/"+gpfHubAPIEgressName, "--namespace", spoke,
	); err != nil {
		return fmt.Errorf("wait for GPF hub API egress ManifestWork: %w", err)
	}
	return nil
}
