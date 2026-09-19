package node

const multinodeConfigTmpl = `multiNode:
  enabled: true
  controlNodeName: "{{.ControlNodeName}}"
`

const networkConfigTmpl = `dns:
  baseDomain: {{.BaseDomain}}
network:
  clusterNetwork:
  - {{.ClusterCIDR}}
  serviceNetwork:
  - {{.ServiceCIDR}}
{{- if .Clients}}
  apiServer:
    subjectAltNames:{{range .Clients}}
    - {{.}}{{end}}
{{- end}}
`

const multusDropinConfig = `[crio.network]
cni_default_network = "multus-cni-network"
plugin_dirs = [
	"/run/cni/bin",
	"/usr/libexec/cni",
]
`
