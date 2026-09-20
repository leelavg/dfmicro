package cluster

const kindnetConfigTmpl = `apiVersion: v1
kind: ConfigMap
metadata:
  name: kindnet-config
  namespace: kube-kindnet
data:
  podSubnet: {{ .ClusterCIDR }}
`
