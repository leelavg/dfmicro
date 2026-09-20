package support

import (
	"fmt"
	"os"
)

const networkConfigTmpl = `dns:
  baseDomain: %s
%snetwork:
  clusterNetwork:
    - %s
  serviceNetwork:
    - %s
`

const powerTuningConfig = `apiServer:
  auditLog:
    profile: None
debugging:
  logLevel: Warning
ingress:
  tuningOptions:
    threadCount: 2
`

const multusDropinConfig = `[crio.network]
# Enable Multus as default CNI and add plugin directories
cni_default_network = "multus-cni-network"
plugin_dirs = [
	"/run/cni/bin",
	"/usr/libexec/cni",
]
`

func WriteNetworkConfig(path, baseDomain, clusterCIDR, serviceCIDR string, clients []string) error {
	var apiServer string
	if len(clients) > 0 {
		apiServer = "apiServer:\n  subjectAltNames:\n"
		for _, client := range clients {
			apiServer += fmt.Sprintf("    - %s\n", client)
		}
	}
	data := fmt.Sprintf(networkConfigTmpl, baseDomain, apiServer, clusterCIDR, serviceCIDR)
	return os.WriteFile(path, []byte(data), 0o644)
}

func WritePowerTuningConfig(path string) error {
	return os.WriteFile(path, []byte(powerTuningConfig), 0o644)
}

func WriteMultusDropin(path string) error {
	return os.WriteFile(path, []byte(multusDropinConfig), 0o644)
}
