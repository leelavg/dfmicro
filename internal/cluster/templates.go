package cluster

const powerTuningConfig = `apiServer:
  auditLog:
    profile: None
debugging:
  logLevel: Warning
ingress:
  tuningOptions:
    threadCount: 2
`

const lvmdConfigTmpl = `apiVersion: v1
kind: ConfigMap
metadata:
  name: topolvm-lvmd-0
  namespace: topolvm-system
data:
  lvmd.yaml: |
        device-classes:
          - name: ssd
            volume-group: {{.Name}}
            type: thin
            spare-gb: 0
            thin-pool:
              name: thin
              overprovision-ratio: {{printf "%.1f" .OverprovisionRatio}}
`

const networkConfigTmpl = `{{- if .Clients }}
apiServer:
  subjectAltNames:
{{- range .Clients }}
    - {{ . }}
{{- end }}
{{- end }}
network:
  clusterNetwork:
    - {{ .ClusterCIDR }}
  serviceNetwork:
    - {{ .ServiceCIDR }}
`

const multusDropinConfig = `[crio.network]
# Enable Multus as default CNI and add plugin directories
cni_default_network = "multus-cni-network"
plugin_dirs = [
	"/run/cni/bin",
	"/usr/libexec/cni",
]
`
