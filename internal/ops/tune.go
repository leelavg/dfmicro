package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const tunedMemoryRequest = "128Mi"
const tunedCPURequest = "50m"

var subscriptionTuneTargets = []struct {
	namespace   string
	packageName string
	requests    map[string]string
}{
	{"openshift-operators", "odf-multicluster-orchestrator", map[string]string{"cpu": tunedCPURequest, "memory": tunedMemoryRequest}},
	{"openshift-operators", "odr-hub-operator", map[string]string{"cpu": tunedCPURequest, "memory": tunedMemoryRequest}},
	{"openshift-storage", "odf-operator", map[string]string{"cpu": "10m", "memory": "256Mi"}},
	{"openshift-storage", "ocs-client-operator", map[string]string{"cpu": "10m", "memory": tunedMemoryRequest}},
}

const (
	odrClusterNamespace = "openshift-dr-system"
	odrClusterPackage   = "odr-cluster-operator"
	odrClusterContainer = "manager"
	odrClusterCPU       = tunedCPURequest
	odrClusterMemory    = tunedMemoryRequest
)

type subscriptionList struct {
	Items []subscriptionItem `json:"items"`
}

type subscriptionItem struct {
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Name   string `json:"name"`
		Config struct {
			Resources map[string]json.RawMessage `json:"resources"`
		} `json:"config"`
	} `json:"spec"`
	Status struct {
		InstalledCSV string `json:"installedCSV"`
	} `json:"status"`
}

type csvDeployments struct {
	Spec struct {
		Install struct {
			Spec struct {
				Deployments []struct {
					Name string `json:"name"`
					Spec struct {
						Template struct {
							Spec struct {
								Containers []struct {
									Name      string          `json:"name"`
									Resources json.RawMessage `json:"resources"`
								} `json:"containers"`
							} `json:"spec"`
						} `json:"template"`
					} `json:"spec"`
				} `json:"deployments"`
			} `json:"spec"`
		} `json:"install"`
	} `json:"spec"`
}

func (r *resources) tune(ctx context.Context) error {
	container, err := r.resolveContainer(ctx)
	if err != nil {
		return err
	}

	for _, target := range subscriptionTuneTargets {
		if err := r.tuneSubscription(ctx, container, target.namespace, target.packageName, target.requests); err != nil {
			return err
		}
	}

	return r.tuneCSVSubscription(ctx, container)
}

func (r *resources) tuneSubscription(ctx context.Context, clusterContainer, namespace, packageName string, requests map[string]string) error {
	result, err := r.oc(ctx, clusterContainer, "get", "subscriptions", "-n", namespace, "-o", "json")
	if err != nil {
		return fmt.Errorf("list subscriptions in %s: %w", namespace, err)
	}
	var list subscriptionList
	if err := json.Unmarshal([]byte(result.Stdout), &list); err != nil {
		return fmt.Errorf("parse subscriptions in %s: %w", namespace, err)
	}

	var found *subscriptionItem
	for i := range list.Items {
		item := &list.Items[i]
		if item.Spec.Name != packageName {
			continue
		}
		if found != nil {
			return fmt.Errorf("multiple subscriptions found for package %s in %s", packageName, namespace)
		}
		found = item
	}
	if found == nil {
		r.logger.Info("resource tuning skipped, subscription not found", "package", packageName, "namespace", namespace)
		return nil
	}

	changed, err := setResourceRequests(&found.Spec.Config.Resources, requests)
	if err != nil {
		return fmt.Errorf("update subscription %s resources: %w", found.Metadata.Name, err)
	}
	if !changed {
		r.logger.Info("resource requests already set", "package", packageName)
		return nil
	}

	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{"config": map[string]any{"resources": found.Spec.Config.Resources}},
	})
	if err != nil {
		return fmt.Errorf("encode subscription resource patch: %w", err)
	}
	if _, err := r.oc(ctx, clusterContainer, "patch", "subscription", found.Metadata.Name, "-n", namespace, "--type=merge", "-p", string(patch)); err != nil {
		return fmt.Errorf("patch subscription %s: %w", found.Metadata.Name, err)
	}
	r.logger.Info("set resource requests", "package", packageName, "requests", requests)
	return nil
}

func setResourceRequests(resources *map[string]json.RawMessage, desired map[string]string) (bool, error) {
	if *resources == nil {
		*resources = map[string]json.RawMessage{}
	}
	var requests map[string]json.RawMessage
	if raw := (*resources)["requests"]; len(raw) != 0 {
		if err := json.Unmarshal(raw, &requests); err != nil {
			return false, fmt.Errorf("parse resource requests: %w", err)
		}
	}
	if requests == nil {
		requests = map[string]json.RawMessage{}
	}
	changed := false
	for name, value := range desired {
		var current string
		if raw := requests[name]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &current); err != nil {
				return false, fmt.Errorf("parse %s request: %w", name, err)
			}
		}
		if current == value {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return false, err
		}
		requests[name] = encoded
		changed = true
	}
	if !changed {
		return false, nil
	}
	encoded, err := json.Marshal(requests)
	(*resources)["requests"] = encoded
	return true, err
}

func (r *resources) tuneCSVSubscription(ctx context.Context, clusterContainer string) error {
	result, err := r.oc(ctx, clusterContainer, "get", "subscriptions", "-n", odrClusterNamespace, "-o", "json")
	if err != nil {
		return fmt.Errorf("list subscriptions in %s: %w", odrClusterNamespace, err)
	}
	var list subscriptionList
	if err := json.Unmarshal([]byte(result.Stdout), &list); err != nil {
		return fmt.Errorf("parse subscriptions in %s: %w", odrClusterNamespace, err)
	}
	var csvName string
	for _, item := range list.Items {
		if item.Spec.Name != odrClusterPackage {
			continue
		}
		if csvName != "" {
			return fmt.Errorf("multiple subscriptions found for package %s in %s", odrClusterPackage, odrClusterNamespace)
		}
		csvName = item.Status.InstalledCSV
	}
	if csvName == "" {
		r.logger.Info("resource tuning skipped, installed subscription not found", "package", odrClusterPackage, "namespace", odrClusterNamespace)
		return nil
	}

	result, err = r.oc(ctx, clusterContainer, "get", "csv", csvName, "-n", odrClusterNamespace, "-o", "json")
	if err != nil {
		return fmt.Errorf("get installed CSV %s: %w", csvName, err)
	}
	var csv csvDeployments
	if err := json.Unmarshal([]byte(result.Stdout), &csv); err != nil {
		return fmt.Errorf("parse installed CSV %s: %w", csvName, err)
	}

	for di, deployment := range csv.Spec.Install.Spec.Deployments {
		for ci, container := range deployment.Spec.Template.Spec.Containers {
			if container.Name != odrClusterContainer {
				continue
			}
			patch, changed, err := csvResourcePatch(di, ci, container.Resources, map[string]string{
				"cpu": odrClusterCPU, "memory": odrClusterMemory,
			})
			if err != nil {
				return fmt.Errorf("prepare CSV %s resource patch: %w", csvName, err)
			}
			if !changed {
				r.logger.Info("resource requests already set", "package", odrClusterPackage, "container", odrClusterContainer)
				return nil
			}
			if _, err := r.oc(ctx, clusterContainer, "patch", "csv", csvName, "-n", odrClusterNamespace, "--type=json", "-p", patch); err != nil {
				return fmt.Errorf("patch CSV %s: %w", csvName, err)
			}
			r.logger.Info("set resource requests in installed CSV", "package", odrClusterPackage, "container", odrClusterContainer, "cpu", odrClusterCPU, "memory", odrClusterMemory)
			return nil
		}
	}
	return fmt.Errorf("container %s not found in installed CSV %s", odrClusterContainer, csvName)
}

func csvResourcePatch(deploymentIndex, containerIndex int, resourcesRaw json.RawMessage, desired map[string]string) (string, bool, error) {
	base := fmt.Sprintf("/spec/install/spec/deployments/%d/spec/template/spec/containers/%d", deploymentIndex, containerIndex)
	if len(resourcesRaw) == 0 || strings.TrimSpace(string(resourcesRaw)) == "null" {
		patch, err := json.Marshal([]map[string]any{{
			"op": "add", "path": base + "/resources", "value": map[string]any{"requests": desired},
		}})
		return string(patch), true, err
	}

	var resources map[string]json.RawMessage
	if err := json.Unmarshal(resourcesRaw, &resources); err != nil {
		return "", false, fmt.Errorf("parse container resources: %w", err)
	}
	requestsRaw, exists := resources["requests"]
	if !exists || strings.TrimSpace(string(requestsRaw)) == "null" {
		patch, err := json.Marshal([]map[string]any{{
			"op": "add", "path": base + "/resources/requests", "value": desired,
		}})
		return string(patch), true, err
	}

	var requests map[string]json.RawMessage
	if err := json.Unmarshal(requestsRaw, &requests); err != nil {
		return "", false, fmt.Errorf("parse container requests: %w", err)
	}
	var operations []map[string]any
	for name, value := range desired {
		var current string
		if raw := requests[name]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &current); err != nil {
				return "", false, fmt.Errorf("parse container %s request: %w", name, err)
			}
		}
		if current == value {
			continue
		}
		operations = append(operations, map[string]any{
			"op": "add", "path": base + "/resources/requests/" + name, "value": value,
		})
	}
	if len(operations) == 0 {
		return "", false, nil
	}
	patch, err := json.Marshal(operations)
	return string(patch), true, err
}
