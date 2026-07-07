package odf

const clusterVersionTmpl = `apiVersion: config.openshift.io/v1
kind: ClusterVersion
metadata:
  name: version
spec:
  channel: {{.Channel}}
  clusterID: microshift-cluster-001
status:
  desired:
    version: {{.Version}}
  history:
  - state: Completed
    version: {{.Version}}
    completionTime: "2026-01-01T00:00:00Z"
  version: {{.Version}}
`

const catalogTmpl = `apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: odf-catsrc
  namespace: openshift-marketplace
spec:
  displayName: OpenShift Data Foundation
  image: {{.CatalogImage}}
  sourceType: grpc
`

const namespaceTmpl = `apiVersion: v1
kind: Namespace
metadata:
  labels:
    openshift.io/cluster-monitoring: "true"
  name: openshift-storage
`

const operatorGroupTmpl = `apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: odf
  namespace: openshift-storage
spec:
  targetNamespaces:
    - openshift-storage
`

const subscriptionTmpl = `apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: {{.SubName}}
  namespace: openshift-storage
spec:
  channel: {{.Channel}}
  name: {{.SubName}}
  source: odf-catsrc
  sourceNamespace: openshift-marketplace
{{- if .SingleNode}}
  config:
    env:
      - name: SINGLE_NODE
        value: "true"
{{- end}}
`

const storageClusterYAML = `apiVersion: ocs.openshift.io/v1
kind: StorageCluster
metadata:
  name: ocs-storagecluster
  namespace: openshift-storage
spec:
  managedResources:
    cephObjectStores:
      reconcileStrategy: ignore
    cephObjectStoreUsers:
      reconcileStrategy: ignore
  multiCloudGateway:
    reconcileStrategy: ignore
  monPVCTemplate:
    spec:
      accessModes:
        - ReadWriteOnce
      resources:
        requests:
          storage: 2Gi
  placement:
    mon: {}
    mds: {}
    mgr: {}
    rbd-mirror: {}
    rgw: {}
    nfs: {}
    noobaa-core: {}
    noobaa-standalone: {}
    osd-prepare: {}
  resources:
    mon:
      requests:
        cpu: 125m
        memory: 128Mi
    mds:
      requests:
        cpu: 125m
        memory: 128Mi
    mgr:
      requests:
        cpu: 125m
        memory: 128Mi
    mgr-sidecar:
      requests:
        cpu: 125m
        memory: 128Mi
    nfs:
      requests:
        cpu: 125m
        memory: 128Mi
    noobaa-core:
      requests:
        cpu: 125m
        memory: 128Mi
    noobaa-db:
      requests:
        cpu: 125m
        memory: 128Mi
    noobaa-db-vol:
      requests:
        storage: 10Gi
    noobaa-endpoint:
      requests:
        cpu: 125m
        memory: 128Mi
    rbd-mirror:
      requests:
        cpu: 125m
        memory: 128Mi
    rgw:
      requests:
        cpu: 125m
        memory: 128Mi
  storageDeviceSets:
    - count: 1
      name: ocs-deviceset
      dataPVCTemplate:
        spec:
          accessModes:
            - ReadWriteOnce
          resources:
            requests:
              storage: 5Gi
          volumeMode: Block
      placement: {}
      portable: false
      replica: 3
      resources:
        requests:
          cpu: 125m
          memory: 128Mi
`
