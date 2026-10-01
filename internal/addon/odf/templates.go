package odf

const packageManifestTmpl = `apiVersion: packages.operators.coreos.com/v1
kind: PackageManifest
metadata:
  name: {{.Package}}
  namespace: openshift-storage
  labels:
    catalog: odf-catsrc
    catalog-namespace: openshift-marketplace
status:
  packageName: {{.Package}}
  catalogSource: odf-catsrc
  catalogSourceNamespace: openshift-marketplace
  defaultChannel: {{.Channel}}
  channels:
    - name: {{.Channel}}
      currentCSV: {{.CSV}}
      entries:
        - name: {{.CSV}}
`

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

const ocsOperatorConfigTmpl = `apiVersion: v1
kind: ConfigMap
metadata:
  name: ocs-operator-config
  namespace: openshift-storage
data:
  ROOK_CURRENT_NAMESPACE_ONLY: "true"
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
`

const storageclusterTmpl = `apiVersion: ocs.openshift.io/v1
kind: StorageCluster
metadata:
  name: ocs-storagecluster
  namespace: openshift-storage
spec:
  enableCephTools: true
  hostNetwork: {{.HostNetwork}}
  network:
    hostNetwork: {{.HostNetwork}}
  monitoring:
    reconcileStrategy: ignore
  managedResources:
    cephCluster:
      mgrCount: 1
    cephObjectStores:
      reconcileStrategy: ignore
    cephObjectStoreUsers:
      reconcileStrategy: ignore
{{- if eq .IncludeCephFS "false"}}
    cephFilesystems:
      reconcileStrategy: ignore
{{- end}}
  multiCloudGateway:
    reconcileStrategy: ignore
  monPVCTemplate:
    spec:
      storageClassName: topolvm-provisioner
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
        cpu: 100m
        memory: 100Mi
    mds:
      requests:
        cpu: 100m
        memory: 100Mi
    mgr:
      requests:
        cpu: 100m
        memory: 100Mi
    mgr-sidecar:
      requests:
        cpu: 100m
        memory: 100Mi
    nfs:
      requests:
        cpu: 100m
        memory: 100Mi
    noobaa-core:
      requests:
        cpu: 100m
        memory: 100Mi
    noobaa-db:
      requests:
        cpu: 100m
        memory: 100Mi
    noobaa-db-vol:
      requests:
        storage: 5Gi
    noobaa-endpoint:
      requests:
        cpu: 100m
        memory: 100Mi
    rbd-mirror:
      requests:
        cpu: 100m
        memory: 100Mi
    rgw:
      requests:
        cpu: 100m
        memory: 100Mi
  storageDeviceSets:
    - count: 1
      name: ocs-deviceset
      dataPVCTemplate:
        spec:
          storageClassName: topolvm-provisioner
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
          cpu: 100m
          memory: 100Mi
`

const rookProviderCephClusterTmpl = `apiVersion: ceph.rook.io/v1
kind: CephCluster
metadata:
  name: ` + providerCephCluster + `
  namespace: ` + providerNamespace + `
spec:
  dataDirHostPath: /var/lib/rook
  cephVersion:
    image: {{.CephImage}}
    allowUnsupported: true
  mon:
    count: 1
    allowMultiplePerNode: true
    volumeClaimTemplate:
      spec:
        storageClassName: topolvm-provisioner
        accessModes:
          - ReadWriteOnce
        resources:
          requests:
            storage: 2Gi
  mgr:
    count: 1
    allowMultiplePerNode: true
  dashboard:
    enabled: false
  crashCollector:
    disable: true
  network:
    hostNetwork: true
    connections:
      requireMsgr2: true
  storage:
    storageClassDeviceSets:
      - name: ` + providerBlockPool + `
        count: 1
        volumeClaimTemplates:
          - metadata:
              name: data
            spec:
              storageClassName: topolvm-provisioner
              volumeMode: Block
              accessModes:
                - ReadWriteOnce
              resources:
                requests:
                  storage: 5Gi
  monitoring:
    enabled: false
  toolbox:
    enabled: true
  cephConfig:
    global:
      osd_pool_default_size: "1"
      mon_warn_on_pool_no_redundancy: "false"
`

const rookProviderBlockPoolTmpl = `apiVersion: ceph.rook.io/v1
kind: CephBlockPool
metadata:
  name: ` + providerBlockPool + `
  namespace: ` + providerNamespace + `
spec:
  failureDomain: host
  replicated:
    size: 1
    requireSafeReplicaSize: false
`

const rookProviderFilesystemTmpl = `apiVersion: ceph.rook.io/v1
kind: CephFilesystem
metadata:
  name: ` + providerFilesystem + `
  namespace: ` + providerNamespace + `
spec:
  metadataPool:
    failureDomain: host
    replicated:
      size: 1
      requireSafeReplicaSize: false
  dataPools:
    - name: ` + providerFilesystemData + `
      failureDomain: host
      replicated:
        size: 1
        requireSafeReplicaSize: false
  metadataServer:
    activeCount: 1
    activeStandby: false
  preservePoolsOnDelete: false
  preserveFilesystemOnDelete: false
`

const externalStorageClusterTmpl = `apiVersion: ocs.openshift.io/v1
kind: StorageCluster
metadata:
  name: ocs-storagecluster
  namespace: openshift-storage
spec:
  enableCephTools: true
  externalStorage:
    enable: true
  monitoring:
    reconcileStrategy: ignore
  multiCloudGateway:
    reconcileStrategy: ignore
`

const externalDetailsSecretTmpl = `apiVersion: v1
kind: Secret
metadata:
  name: rook-ceph-external-cluster-details
  namespace: openshift-storage
type: Opaque
stringData:
  external_cluster_details: |-
    {{.Details}}
`
