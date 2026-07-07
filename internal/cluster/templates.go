package cluster

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
