package cluster

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed topolvm-assets/*
var topolvmAssets embed.FS

func TopoLVMManifestDir(cfg Config) string {
	return filepath.Join(cfg.StateDir, "topolvm")
}

func WriteTopoLVMManifest(cfg Config, nodes []string) error {
	dir := TopoLVMManifestDir(cfg)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	for _, name := range []string{
		"01-namespace.yaml",
		"02-topolvm.yaml",
		"topolvm_mutatingwebhook_patch.yaml",
		"topolvm_service_patch.yaml",
	} {
		data, err := fs.ReadFile(topolvmAssets, "topolvm-assets/"+name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}

	var lvmd strings.Builder
	fmt.Fprintf(&lvmd, `apiVersion: v1
kind: ConfigMap
metadata:
  name: topolvm-lvmd-0
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
`, cfg.VGName, cfg.OverprovisionRatio)
	if err := os.WriteFile(filepath.Join(dir, "03-lvmd.yaml"), []byte(lvmd.String()), 0o644); err != nil {
		return err
	}
	controlPatch := fmt.Sprintf(`apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: topolvm-lvmd-0
  namespace: topolvm-system
spec:
  template:
    spec:
      nodeSelector:
        kubernetes.io/hostname: %s
`, nodes[0])
	if err := os.WriteFile(filepath.Join(dir, "dfmicro-topolvm-control-patch.yaml"), []byte(controlPatch), 0o644); err != nil {
		return err
	}

	var workers strings.Builder
	for index, node := range nodes[1:] {
		if index > 0 {
			workers.WriteString("---\n")
		}
		fmt.Fprintf(&workers, `apiVersion: v1
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
	if err := os.WriteFile(filepath.Join(dir, "04-dfmicro-topolvm.yaml"), []byte(workers.String()), 0o644); err != nil {
		return err
	}

	resources := "  - 01-namespace.yaml\n  - 02-topolvm.yaml\n  - 03-lvmd.yaml\n"
	if len(nodes) > 1 {
		resources += "  - 04-dfmicro-topolvm.yaml\n"
	}
	kustomization := fmt.Sprintf(`apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
%spatches:
  - path: dfmicro-topolvm-control-patch.yaml
  - path: topolvm_mutatingwebhook_patch.yaml
  - path: topolvm_service_patch.yaml
`, resources)
	return os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte(kustomization), 0o644)
}
