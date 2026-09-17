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

const networkConfigTmpl = `dns:
  baseDomain: {{ .BaseDomain }}
{{ if .Clients }}
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

const kindnetConfigTmpl = `apiVersion: v1
kind: ConfigMap
metadata:
  name: kindnet-config
  namespace: kube-kindnet
data:
  podSubnet: {{ .ClusterCIDR }}
`

const multusDropinConfig = `[crio.network]
# Enable Multus as default CNI and add plugin directories
cni_default_network = "multus-cni-network"
plugin_dirs = [
	"/run/cni/bin",
	"/usr/libexec/cni",
]
`
