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
	hubName      string
	hubNamespace string
	spokeName    string
	appManager   bool
}

// The hub and spoke CLIs use different names for some addons.
var supportedOCMAddons = map[string]ocmAddon{
	"application-manager":         {spokeName: "application-manager", appManager: true},
	"argocd":                      {hubName: "argocd", hubNamespace: "argocd", spokeName: "argocd"},
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
	var argocd, argocdAgent bool
	for _, name := range names {
		name = strings.TrimSpace(name)
		if _, ok := supportedOCMAddons[name]; !ok {
			return fmt.Errorf("unsupported OCM addon %q; supported addons: %s", name, strings.Join(supportedOCMAddonNames, ", "))
		}
		switch name {
		case "argocd":
			argocd = true
		case "argocd-agent":
			argocdAgent = true
		}
	}
	if argocd && argocdAgent {
		return fmt.Errorf("argocd and argocd-agent are alternative OCM integrations; choose one")
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

	hubAddonsByNamespace := make(map[string][]string)
	hubAddonNamespaces := make([]string, 0, len(addonNames))
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
				if _, ok := hubAddonsByNamespace[addon.hubNamespace]; !ok {
					hubAddonNamespaces = append(hubAddonNamespaces, addon.hubNamespace)
				}
				hubAddonsByNamespace[addon.hubNamespace] = append(hubAddonsByNamespace[addon.hubNamespace], addon.hubName)
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
	var tempDir string
	var hubPrivateKubeconfig, contextName string
	if len(hubAddonNamespaces) > 0 || len(spokeNames) > 0 {
		tempDir, err = os.MkdirTemp("", "dfmicro-ocm-")
		if err != nil {
			return fmt.Errorf("create temporary OCM config directory: %w", err)
		}
		defer os.RemoveAll(tempDir)
		hubPrivateKubeconfig = filepath.Join(tempDir, "hub-kubeconfig")
		contextName, err = privateContext(ctx, o.runner, hubKubeconfig, hubPrivateKubeconfig)
		if err != nil {
			return err
		}
	}

	if installApplicationManager {
		o.logger.Info("installing OCM addon on hub", "addon", "application-manager", "cluster", hubName)
		if err := support.ApplyYAML(ctx, o.runner, support.KubeClient, hubKubeconfig, applicationManagerManifest); err != nil {
			return fmt.Errorf("install application-manager on hub: %w", err)
		}
	}
	for _, namespace := range hubAddonNamespaces {
		addons := hubAddonsByNamespace[namespace]
		o.logger.Info("installing OCM addons on hub", "addons", addons, "cluster", hubName, "namespace", namespace)
		args := []string{
			"--kubeconfig", hubPrivateKubeconfig, "--context", contextName,
			"install", "hub-addon", "--names=" + strings.Join(addons, ","),
		}
		if namespace != "" {
			args = append(args, "--namespace="+namespace, "--create-namespace")
		}
		if _, err := execx.Run(ctx, o.runner, clusteradm, args...); err != nil {
			return fmt.Errorf("install OCM addons on hub: %w", err)
		}
	}

	if len(spokeNames) == 0 {
		return nil
	}

	for i, spoke := range spokeNames {
		managedCluster, err := execx.Run(ctx, o.runner, support.KubeClient,
			"--kubeconfig", hubPrivateKubeconfig, "--context", contextName,
			"get", "managedcluster", spoke, "--ignore-not-found", "-o", "name")
		if err != nil {
			return fmt.Errorf("check OCM spoke %q: %w", spoke, err)
		}
		if strings.TrimSpace(managedCluster.Stdout) != "" {
			o.logger.Info("OCM spoke already joined", "hub", hubName, "spoke", spoke)
			continue
		}

		spokePrivateKubeconfig := filepath.Join(tempDir, fmt.Sprintf("spoke-%d-kubeconfig", i))
		spokeContext, err := privateContext(ctx, o.runner, spokeKubeconfigs[spoke], spokePrivateKubeconfig)
		if err != nil {
			return fmt.Errorf("select private API context for spoke %q: %w", spoke, err)
		}

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
		o.logger.Info("joining OCM spoke", "hub", hubName, "spoke", spoke, "hub-context", contextName, "spoke-context", spokeContext)
		args := []string{
			"--kubeconfig", spokePrivateKubeconfig,
			"join",
			"--hub-token", token.HubToken,
			"--hub-apiserver", token.HubAPIServer,
			"--cluster-name", spoke,
			"--singleton",
			"--wait",
		}
		if err := o.runner.RunInteractive(ctx, clusteradm, args...); err != nil {
			return fmt.Errorf("join OCM spoke %q: %w", spoke, err)
		}
		o.logger.Info("OCM spoke joined", "hub", hubName, "spoke", spoke)
	}

	if len(spokeAddons) > 0 {
		o.logger.Info("enabling OCM addons on spokes", "addons", spokeAddons, "spokes", spokeNames)
		args := []string{
			"--kubeconfig", hubPrivateKubeconfig,
			"--context", contextName,
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
