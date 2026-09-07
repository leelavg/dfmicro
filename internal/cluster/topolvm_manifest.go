package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func TopoLVMManifestPath(cfg Config) string {
	return filepath.Join(cfg.StateDir, "04-dfmicro-topolvm.yaml")
}

func topoLVMKustomizationPath(cfg Config) string {
	return filepath.Join(cfg.StateDir, "kustomization.yaml")
}

func topoLVMKustomizationPatchPath(cfg Config) string {
	return filepath.Join(cfg.StateDir, "dfmicro-topolvm-patch.yaml")
}

func WriteTopoLVMManifest(cfg Config, nodes []string) error {
	var resources strings.Builder
	for index, node := range nodes[1:] {
		if index > 0 {
			resources.WriteString("---\n")
		}
		fmt.Fprintf(&resources, `apiVersion: v1
kind: ConfigMap
metadata:
  name: topolvm-lvmd-%d
  namespace: topolvm-system
data:
  lvmd.yaml: |
    device-classes:
      - name: ssd
        volume-group: %s
        default: true
        type: thin
        spare-gb: 0
        thin-pool:
          name: thin
          overprovision-ratio: %.1f
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: topolvm-lvmd-%d
  namespace: topolvm-system
  labels:
    app.kubernetes.io/component: lvmd
    app.kubernetes.io/instance: topolvm
    app.kubernetes.io/name: topolvm
    idx: "%d"
spec:
  selector:
    matchLabels:
      app.kubernetes.io/component: lvmd
      app.kubernetes.io/instance: topolvm
      app.kubernetes.io/name: topolvm
      idx: "%d"
  template:
    metadata:
      labels:
        app.kubernetes.io/component: lvmd
        app.kubernetes.io/instance: topolvm
        app.kubernetes.io/name: topolvm
        idx: "%d"
    spec:
      nodeSelector:
        kubernetes.io/hostname: %s
      serviceAccountName: topolvm-lvmd
      hostPID: true
      containers:
      - name: lvmd
        image: ghcr.io/topolvm/topolvm-with-sidecar:0.36.2
        command: ["/lvmd"]
        securityContext:
          privileged: true
        volumeMounts:
        - name: devices-dir
          mountPath: /dev
        - name: config
          mountPath: /etc/topolvm
        - name: lvmd-socket-dir
          mountPath: /run/topolvm
      volumes:
      - name: devices-dir
        hostPath:
          path: /dev
          type: Directory
      - name: config
        configMap:
          name: topolvm-lvmd-%d
      - name: lvmd-socket-dir
        hostPath:
          path: /run/topolvm
          type: DirectoryOrCreate
`, index+1, node, cfg.OverprovisionRatio, index+1, index+1, index+1, index+1, node, index+1)
	}

	if err := os.WriteFile(TopoLVMManifestPath(cfg), []byte(resources.String()), 0o644); err != nil {
		return err
	}

	controlNode := nodes[0]
	patch := fmt.Sprintf(`apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: topolvm-lvmd-0
  namespace: topolvm-system
spec:
  template:
    spec:
      nodeSelector:
        kubernetes.io/hostname: %s
`, controlNode)
	if err := os.WriteFile(topoLVMKustomizationPatchPath(cfg), []byte(patch), 0o644); err != nil {
		return err
	}

	resourcesList := "  - 01-namespace.yaml\n  - 02-topolvm.yaml\n  - 03-lvmd.yaml\n"
	if len(nodes) > 1 {
		resourcesList += "  - 04-dfmicro-topolvm.yaml\n"
	}
	kustomization := fmt.Sprintf(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
%spatches:
  - path: dfmicro-topolvm-patch.yaml
  - path: topolvm_mutatingwebhook_patch.yaml
  - path: topolvm_service_patch.yaml
`, resourcesList)
	return os.WriteFile(topoLVMKustomizationPath(cfg), []byte(kustomization), 0o644)
}
