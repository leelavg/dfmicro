package odf

const packageManifestTmpl = `apiVersion: packages.operators.coreos.com/v1
kind: PackageManifest
metadata:
  name: {{.Package}}
  namespace: openshift-storage
  labels:
    app.kubernetes.io/created-by: dfmicro
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
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  channel: {{.Channel}}
  clusterID: {{.ClusterID}}
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
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  displayName: OpenShift Data Foundation
  image: {{.CatalogImage}}
  sourceType: grpc
`

const namespaceTmpl = `apiVersion: v1
kind: Namespace
metadata:
  labels:
    app.kubernetes.io/created-by: dfmicro
    openshift.io/cluster-monitoring: "true"
  name: openshift-storage
`

const operatorGroupTmpl = `apiVersion: operators.coreos.com/v1
kind: OperatorGroup
metadata:
  name: odf
  namespace: openshift-storage
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  targetNamespaces:
    - openshift-storage
`

const ocsOperatorConfigTmpl = `apiVersion: v1
kind: ConfigMap
metadata:
  name: ocs-operator-config
  namespace: openshift-storage
  labels:
    app.kubernetes.io/created-by: dfmicro
data:
  ROOK_CURRENT_NAMESPACE_ONLY: "true"
`

const subscriptionTmpl = `apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: {{.SubName}}
  namespace: openshift-storage
  labels:
    app.kubernetes.io/created-by: dfmicro
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
  labels:
    app.kubernetes.io/created-by: dfmicro
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

const rookProviderSccTmpl = `apiVersion: security.openshift.io/v1
kind: SecurityContextConstraints
metadata:
  name: rook-ceph
  labels:
    app.kubernetes.io/created-by: dfmicro
allowPrivilegedContainer: true
allowHostDirVolumePlugin: true
allowHostIPC: true
allowHostNetwork: true
allowHostPorts: true
allowedCapabilities:
  - MKNOD
  - SYS_ADMIN
requiredDropCapabilities:
  - ALL
runAsUser:
  type: RunAsAny
seLinuxContext:
  type: MustRunAs
fsGroup:
  type: MustRunAs
supplementalGroups:
  type: RunAsAny
volumes:
  - configMap
  - downwardAPI
  - emptyDir
  - hostPath
  - persistentVolumeClaim
  - projected
  - secret
users:
  - system:serviceaccount:` + providerNamespace + `:rook-ceph-system
  - system:serviceaccount:` + providerNamespace + `:rook-ceph-default
  - system:serviceaccount:` + providerNamespace + `:rook-ceph-mgr
  - system:serviceaccount:` + providerNamespace + `:rook-ceph-osd
  - system:serviceaccount:` + providerNamespace + `:rook-ceph-rgw
  - system:serviceaccount:` + providerNamespace + `:rook-ceph-nvmeof
`

const rookProviderCephClusterTmpl = `apiVersion: ceph.rook.io/v1
kind: CephCluster
metadata:
  name: {{.CephCluster}}
  namespace: ` + providerNamespace + `
  labels:
    app.kubernetes.io/created-by: dfmicro
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
      - name: {{.BlockPool}}
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
    enabled: true
  cephConfig:
    global:
      osd_pool_default_size: "1"
      mon_warn_on_pool_no_redundancy: "false"
`

const rookProviderToolboxTmpl = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: rook-ceph-tools
  namespace: ` + providerNamespace + `
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  replicas: 1
  selector:
    matchLabels:
      app: rook-ceph-tools
  template:
    metadata:
      labels:
        app: rook-ceph-tools
      annotations:
        openshift.io/required-scc: rook-ceph
    spec:
      serviceAccountName: rook-ceph-default
      hostNetwork: true
      dnsPolicy: ClusterFirstWithHostNet
      containers:
        - name: rook-ceph-tools
          image: {{.ToolboxImage}}
          command:
            - /bin/bash
          args:
            - -m
            - -c
            - /usr/local/bin/toolbox.sh
          tty: true
          securityContext:
            runAsNonRoot: true
            runAsUser: 2016
            runAsGroup: 2016
          env:
            - name: ROOK_CEPH_USERNAME
              valueFrom:
                secretKeyRef:
                  name: rook-ceph-mon
                  key: ceph-username
          volumeMounts:
            - name: ceph-config
              mountPath: /etc/ceph
            - name: mon-endpoint-volume
              mountPath: /etc/rook
            - name: ceph-admin-secret
              mountPath: /var/lib/rook-ceph-mon
              readOnly: true
            - name: external-cluster-script
              mountPath: /var/run/rook/external-cluster-script
              readOnly: true
      volumes:
        - name: ceph-config
          emptyDir: {}
        - name: mon-endpoint-volume
          configMap:
            name: rook-ceph-mon-endpoints
            items:
              - key: data
                path: mon-endpoints
        - name: ceph-admin-secret
          secret:
            secretName: rook-ceph-mon
            items:
              - key: ceph-secret
                path: secret.keyring
        - name: external-cluster-script
          configMap:
            name: rook-ceph-external-cluster-script-config
            optional: true
            items:
              - key: script
                path: script.py
`

const rookProviderBlockPoolTmpl = `apiVersion: ceph.rook.io/v1
kind: CephBlockPool
metadata:
  name: {{.BlockPool}}
  namespace: ` + providerNamespace + `
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  failureDomain: host
  replicated:
    size: 1
    requireSafeReplicaSize: false
`

const rookProviderFilesystemTmpl = `apiVersion: ceph.rook.io/v1
kind: CephFilesystem
metadata:
  name: {{.Filesystem}}
  namespace: ` + providerNamespace + `
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  metadataPool:
    failureDomain: host
    replicated:
      size: 1
      requireSafeReplicaSize: false
  dataPools:
    - name: {{.FilesystemData}}
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
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
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
  labels:
    app.kubernetes.io/created-by: dfmicro
type: Opaque
stringData:
  external_cluster_details: |-
    {{.Details}}
`

const storageConsumerTmpl = `apiVersion: ocs.openshift.io/v1alpha1
kind: StorageConsumer
metadata:
  name: {{.ClientCluster}}
  namespace: openshift-storage
  labels:
    app.kubernetes.io/created-by: dfmicro
`

const storageClientTmpl = `apiVersion: ocs.openshift.io/v1alpha1
kind: StorageClient
metadata:
  name: {{.ProviderCluster}}
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  onboardingTicket: {{.Ticket}}
  storageProviderEndpoint: {{.Endpoint}}
`
