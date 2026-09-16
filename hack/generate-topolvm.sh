#!/usr/bin/env bash
set -euo pipefail

# Requires curl, helm, and yq. The generated assets are checked in so these
# tools are only needed when the chart version changes.

version="${TOPOLVM_VERSION:?TOPOLVM_VERSION is required}"
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
assets_dir="${script_dir}/../internal/cluster/topolvm-assets"
marker="${assets_dir}/.topolvm-version"
chart_url="https://github.com/topolvm/topolvm/releases/download/topolvm-chart-v${version}/topolvm-${version}.tgz"

expected=(
	01-namespace.yaml
	02-topolvm.yaml
	03-lvmd.yaml
	topolvm-image
	kustomization.yaml
)
if [[ -f "${marker}" ]] && [[ "$(<"${marker}")" == "${version}" ]]; then
	rm -f "${assets_dir}/topolvm_mutatingwebhook_patch.yaml" "${assets_dir}/topolvm_service_patch.yaml"
	ready=true
	for file in "${expected[@]}"; do
		if [[ ! -f "${assets_dir}/${file}" ]]; then
			ready=false
			break
		fi
	done
	if [[ "${ready}" == true ]]; then
		echo "TopoLVM ${version} manifests are up to date"
		exit 0
	fi
fi

missing=()
for dependency in curl helm yq; do
	if ! command -v "${dependency}" >/dev/null 2>&1; then
		missing+=("${dependency}")
	fi
done
if ((${#missing[@]} > 0)); then
	echo "error: missing required tools: ${missing[*]}" >&2
	exit 1
fi

tmp_dir="$(mktemp -d)"
trap 'rm -rf "${tmp_dir}"' EXIT
chart="${tmp_dir}/topolvm-${version}.tgz"
rendered="${tmp_dir}/assets"
mkdir -p "${rendered}"

curl -fsSL --retry 5 "${chart_url}" -o "${chart}"
helm template topolvm "${chart}" \
	--include-crds \
	--namespace=topolvm-system \
	--set cert-manager.enabled=false \
	--set webhook.podMutatingWebhook.enabled=false \
	--set webhook.caBundle=dummy \
	--set webhook.tlsSecretName=topolvm-webhook-cert \
	>"${rendered}/02-topolvm.yaml"

cat >"${rendered}/01-namespace.yaml" <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: topolvm-system
  labels:
    openshift.io/run-level: "0"
    pod-security.kubernetes.io/enforce: privileged
    pod-security.kubernetes.io/audit: privileged
    pod-security.kubernetes.io/warn: privileged
EOF

yq -i 'del(.webhooks.0.clientConfig.caBundle)' "${rendered}/02-topolvm.yaml"
yq 'select(.kind == "ConfigMap" and .metadata.name == "topolvm-lvmd-0")' \
	"${rendered}/02-topolvm.yaml" >"${rendered}/03-lvmd.yaml"
yq -i 'del(select(.kind == "ConfigMap" and .metadata.name == "topolvm-lvmd-0"))' \
	"${rendered}/02-topolvm.yaml"
yq -i 'select(.kind == "Deployment").spec.replicas = 1' "${rendered}/02-topolvm.yaml"
yq -i 'with(select(.kind == "StorageClass" and .metadata.name == "topolvm-provisioner"); .metadata.annotations."storageclass.kubernetes.io/is-default-class" = "true")' \
	"${rendered}/02-topolvm.yaml"
yq -i 'with(select(.kind == "Deployment" and .metadata.name == "topolvm-controller").spec.template.spec.containers[] | select(.name == "topolvm-controller"); .livenessProbe.failureThreshold = 3 | .readinessProbe.timeoutSeconds = 3 | .readinessProbe.failureThreshold = 3 | .readinessProbe.periodSeconds = 60 | .startupProbe = {"failureThreshold": 3, "periodSeconds": 60, "timeoutSeconds": 3, "httpGet": {"port": "healthz", "path": "/healthz"}})' \
	"${rendered}/02-topolvm.yaml"
yq -i 'with(select(.kind == "DaemonSet" and .metadata.name == "topolvm-node").spec.template.spec.containers[] | select(.name == "topolvm-node"); .startupProbe = {"failureThreshold": 60, "periodSeconds": 2, "timeoutSeconds": 3, "httpGet": {"port": "healthz", "path": "/healthz"}})' \
	"${rendered}/02-topolvm.yaml"
yq -r 'select(.kind == "DaemonSet" and .metadata.name == "topolvm-lvmd-0").spec.template.spec.containers[] | select(.name == "lvmd").image' \
	"${rendered}/02-topolvm.yaml" >"${rendered}/topolvm-image"

cat >"${rendered}/kustomization.yaml" <<'EOF'
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - 01-namespace.yaml
  - 02-topolvm.yaml
  - 03-lvmd.yaml
EOF

mkdir -p "${assets_dir}"
for file in "${expected[@]}"; do
	mv "${rendered}/${file}" "${assets_dir}/${file}"
done
printf '%s\n' "${version}" >"${marker}"
echo "Generated TopoLVM ${version} manifests in ${assets_dir}"
