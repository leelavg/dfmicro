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
  monitoring:
    reconcileStrategy: ignore
  managedResources:
    cephObjectStores:
      reconcileStrategy: ignore
    cephObjectStoreUsers:
      reconcileStrategy: ignore
{{- if not .IncludeCephFS}}
    cephFilesystems:
      reconcileStrategy: ignore
{{- end}}
  multiCloudGateway:
    reconcileStrategy: ignore
  monPVCTemplate:
    spec:
      storageClassName: topolvm-provisioner-immediate
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
          storageClassName: topolvm-provisioner-immediate
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
