package network

const nadTemplate = `apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  config: |
    {
      "cniVersion": "0.3.1",
      "name": "{{.Bridge}}",
      "type": "macvlan",
      "master": "eth1",
      "mode": "bridge",
      "ipam": {
        "type": "host-local",
        "subnet": "{{.Subnet}}",
        "rangeStart": "{{.RangeStart}}",
        "rangeEnd": "{{.RangeEnd}}"
      }
    }
`
