package ocm

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	rootconfig "dfmicro/internal/config"
	"dfmicro/internal/execx"
	"dfmicro/internal/support"
)

//go:embed assets/application-manager.yaml
var applicationManagerManifest string

type ocmAddon struct {
	hubName    string
	spokeName  string
	appManager bool
}

// The hub and spoke CLIs use different names for some addons.
var supportedOCMAddons = map[string]ocmAddon{
	"application-manager":         {spokeName: "application-manager", appManager: true},
	"argocd":                      {hubName: "argocd", spokeName: "argocd"},
	"argocd-agent":                {hubName: "argocd-agent", spokeName: "argocd-agent-addon"},
	"config-policy-controller":    {hubName: "governance-policy-framework", spokeName: "config-policy-controller"},
	"governance-policy-framework": {hubName: "governance-policy-framework", spokeName: "governance-policy-framework"},
}

var supportedOCMAddonNames = []string{
	"application-manager",
	"argocd",
	"argocd-agent",
	"config-policy-controller",
	"governance-policy-framework",
}

func (o *ocm) listAddons() error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "DFMICRO ADDON\tHUB INSTALL\tSPOKE ENABLE")
	for _, name := range supportedOCMAddonNames {
		addon := supportedOCMAddons[name]
		hubName := addon.hubName
		if addon.appManager {
			hubName = "embedded bundle"
		}
		spokeName := addon.spokeName
		if spokeName == "" {
			spokeName = "hub only"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", name, hubName, spokeName)
	}
	return w.Flush()
}

func validateOCMSpokes(names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return fmt.Errorf("spoke name cannot be empty")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("duplicate spoke name: %s", name)
		}
		seen[name] = struct{}{}
	}
	return nil
}

func validateOCMAddons(names []string) error {
	for _, name := range names {
		if _, ok := supportedOCMAddons[strings.TrimSpace(name)]; !ok {
			return fmt.Errorf("unsupported OCM addon %q; supported addons: %s", name, strings.Join(supportedOCMAddonNames, ", "))
		}
	}
	return nil
}

func (o *ocm) configure(ctx context.Context, hubName string, spokeNames, addonNames []string) error {
	for i, name := range spokeNames {
		spokeNames[i] = strings.TrimSpace(name)
	}

	hubKubeconfig, err := rootconfig.Kubeconfig(hubName)
	if err != nil {
		return fmt.Errorf("could not load kubeconfig for hub cluster %q: %w", hubName, err)
	}

	hubAddons := make([]string, 0, len(addonNames))
	spokeAddons := make([]string, 0, len(addonNames))
	hubSeen := make(map[string]struct{})
	spokeAddonSeen := make(map[string]struct{})
	installApplicationManager := false
	for _, name := range addonNames {
		name = strings.TrimSpace(name)
		addon := supportedOCMAddons[name]
		if addon.appManager {
			installApplicationManager = true
		}
		if addon.hubName != "" {
			if _, ok := hubSeen[addon.hubName]; !ok {
				hubSeen[addon.hubName] = struct{}{}
				hubAddons = append(hubAddons, addon.hubName)
			}
		}
		if addon.spokeName != "" {
			if _, ok := spokeAddonSeen[addon.spokeName]; !ok {
				spokeAddonSeen[addon.spokeName] = struct{}{}
				spokeAddons = append(spokeAddons, addon.spokeName)
			}
		}
	}

	spokeKubeconfigs := make(map[string]string, len(spokeNames))
	for _, spoke := range spokeNames {
		kubeconfig, err := rootconfig.Kubeconfig(spoke)
		if err != nil {
			return fmt.Errorf("could not load kubeconfig for spoke cluster %q: %w", spoke, err)
		}
		spokeKubeconfigs[spoke] = kubeconfig
	}

	clusteradm := support.ClusteradmPath()
	if installApplicationManager {
		o.logger.Info("installing OCM addon on hub", "addon", "application-manager", "cluster", hubName)
		if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, hubKubeconfig, applicationManagerManifest); err != nil {
			return fmt.Errorf("install application-manager on hub: %w", err)
		}
	}
	if len(hubAddons) > 0 {
		o.logger.Info("installing OCM addons on hub", "addons", hubAddons, "cluster", hubName)
		args := []string{"--kubeconfig", hubKubeconfig, "install", "hub-addon", "--names=" + strings.Join(hubAddons, ",")}
		if _, err := execx.Run(ctx, o.runner, clusteradm, args...); err != nil {
			return fmt.Errorf("install OCM addons on hub: %w", err)
		}
	}

	if len(spokeNames) == 0 {
		return nil
	}
	tempDir, err := os.MkdirTemp("", "dfmicro-ocm-")
	if err != nil {
		return fmt.Errorf("create temporary OCM config directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	hubPrivateKubeconfig := filepath.Join(tempDir, "hub-kubeconfig")
	contextName, err := privateContext(ctx, o.runner, hubKubeconfig, hubPrivateKubeconfig)
	if err != nil {
		return err
	}

	for _, spoke := range spokeNames {
		tokenResult, err := execx.Run(ctx, o.runner, clusteradm, "--kubeconfig", hubPrivateKubeconfig,
			"--context", contextName, "get", "token", "--output=json")
		if err != nil {
			return fmt.Errorf("get OCM token for spoke %q: %w", spoke, err)
		}
		var token struct {
			HubToken     string `json:"hub-token"`
			HubAPIServer string `json:"hub-apiserver"`
		}
		if err := json.Unmarshal([]byte(tokenResult.Stdout), &token); err != nil {
			return fmt.Errorf("decode OCM token for spoke %q: %w", spoke, err)
		}
		if token.HubToken == "" || token.HubAPIServer == "" {
			return fmt.Errorf("OCM token response for spoke %q is missing hub-token or hub-apiserver", spoke)
		}

		o.logger.Info("joining OCM spoke", "hub", hubName, "spoke", spoke, "hub-context", contextName)
		args := []string{
			"--kubeconfig", spokeKubeconfigs[spoke],
			"join",
			"--hub-token", token.HubToken,
			"--hub-apiserver", token.HubAPIServer,
			"--cluster-name", spoke,
			"--singleton",
			"--wait",
		}
		if _, err := execx.Run(ctx, o.runner, clusteradm, args...); err != nil {
			return fmt.Errorf("join OCM spoke %q: %w", spoke, err)
		}
		o.logger.Info("OCM spoke joined", "hub", hubName, "spoke", spoke)
	}

	if len(spokeAddons) > 0 {
		o.logger.Info("enabling OCM addons on spokes", "addons", spokeAddons, "spokes", spokeNames)
		args := []string{
			"--kubeconfig", hubKubeconfig,
			"addon", "enable",
			"--names=" + strings.Join(spokeAddons, ","),
			"--clusters=" + strings.Join(spokeNames, ","),
		}
		if _, err := execx.Run(ctx, o.runner, clusteradm, args...); err != nil {
			return fmt.Errorf("enable OCM addons on spokes: %w", err)
		}
	}

	return nil
}
