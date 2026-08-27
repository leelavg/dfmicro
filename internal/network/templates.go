package network

const nadTemplate = `apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
  name: {{.Name}}
  namespace: {{.Namespace}}
spec:
  config: |
    {
      "cniVersion": "1.0.0",
      "name": "{{.Bridge}}",
      "type": "macvlan",
      "master": "{{.Master}}",
      "mode": "bridge",
      "ipam": {
        "type": "host-local",
        "ranges": [
          [
            {
              "subnet": "{{.Subnet}}",
              "rangeStart": "{{.RangeStart}}",
              "rangeEnd": "{{.RangeEnd}}"
            }
          ]
        ]
      }
    }
`
