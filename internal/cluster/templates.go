package cluster

const kindnetConfigTmpl = `apiVersion: v1
kind: ConfigMap
metadata:
  name: kindnet-config
  namespace: kube-kindnet
  labels:
    app.kubernetes.io/created-by: dfmicro
data:
  podSubnet: {{ .ClusterCIDR }}
`
