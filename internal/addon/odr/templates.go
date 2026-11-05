package odr

const catalogSourceTmpl = `apiVersion: operators.coreos.com/v1alpha1
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

const subscriptionTmpl = `apiVersion: operators.coreos.com/v1alpha1
kind: Subscription
metadata:
  name: odf-multicluster-orchestrator
  namespace: openshift-operators
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  channel: {{.Channel}}
  name: odf-multicluster-orchestrator
  source: odf-catsrc
  sourceNamespace: openshift-marketplace
`

const trustedCABundleTmpl = `apiVersion: v1
kind: ConfigMap
metadata:
  name: openshift-trusted-cabundle
  namespace: openshift-operators
  labels:
    app.kubernetes.io/created-by: dfmicro
data:
  ca-bundle.crt: {{.CABundle}}
`

const localS3SecretTmpl = `apiVersion: v1
kind: Secret
metadata:
  name: dfmicro-odr-s3
  namespace: openshift-operators
  labels:
    app.kubernetes.io/created-by: dfmicro
    multicluster.odf.openshift.io/created-by: s3config
type: Opaque
stringData:
  AWS_ACCESS_KEY_ID: dummy
  AWS_SECRET_ACCESS_KEY: dummy
  s3Bucket: {{.Bucket}}
  s3CompatibleEndpoint: http://s3.default.svc:9000
  s3Region: us-east-1
`

const s3ConfigurationTmpl = `apiVersion: multicluster.odf.openshift.io/v1alpha1
kind: S3Configuration
metadata:
  name: dfmicro-odr
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  externalS3:
    secretRef:
      name: dfmicro-odr-s3
      namespace: openshift-operators
  managedClusters: [{{.ManagedClusters}}]
`

const mirrorPeerTmpl = `apiVersion: multicluster.odf.openshift.io/v1alpha1
kind: MirrorPeer
metadata:
  name: dfmicro-odr
  labels:
    app.kubernetes.io/created-by: dfmicro
spec:
  storageVendor: odf
  type: {{.Type}}
  items:
    - clusterName: {{.ClusterA}}
      storageClusterRef:
        name: ocs-storagecluster
        namespace: openshift-storage
    - clusterName: {{.ClusterB}}
      storageClusterRef:
        name: ocs-storagecluster
        namespace: openshift-storage
`
