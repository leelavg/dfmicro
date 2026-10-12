# Examples

Run `dfmicro` as a normal user after creating its sudoers rules. The examples assume a MicroShift image, `~/pull.json`, and `~/idms.yaml` are available.

## Prepare access

```console
dfmicro ops sudoers create
```

Inspect the CLI build and embedded defaults.

The examples below were verified with this build and configuration:

```console
$ dfmicro -v
dfmicro version dev (none, 2026-10-11T07:14:30Z)
$ dfmicro config
{
  "name": "micro",
  "image": "ghcr.io/leelavg/microshift:5.0.0_202609201132_g4582bf673_5.0.0_okd_scos.ec.8",
  "powerTuning": true,
  "apiServerPort": 6443,
  "clusterCIDR": "10.42.0.0/16",
  "serviceCIDR": "10.43.0.0/16",
  "exposeKubeAPI": true,
  "lvmVolSize": "10G",
  "overprovisionRatio": 20,
  "shareHostContainers": true,
  "enableThinpool": true,
  "enableTopoLVM": true,
  "useEtcd": false,
  "bridgeName": "micro",
  "bridgeSubnet": "172.20.0.0/24",
  "groupCount": 5,
  "clustersPerGroup": 5,
  "reservePerGroup": 5,
  "nadNamespace": "default"
}
```

## Create and inspect a cluster

```console
dfmicro cluster create \
  --name demo \
  --no-topolvm \
  --pull-secret ~/pull.json \
  --idms ~/idms.yaml

dfmicro cluster list
dfmicro cluster config --name demo
dfmicro node config --cluster demo
dfmicro cluster kubeconfig --name demo >/tmp/demo-kubeconfig
export KUBECONFIG=/tmp/demo-kubeconfig

kubectl get nodes -o wide
kubectl get pods -A
```

Stop and start the cluster without removing its state.

```console
dfmicro cluster stop --name demo
dfmicro cluster start --name demo
```

## Add and remove a worker

Add a worker after the control plane is ready. The command checks control-plane readiness before onboarding the worker.

```console
dfmicro node add --cluster demo
kubectl get nodes -o wide
kubectl get pods -A -o wide
sudo podman exec demo-2 journalctl -u microshift --no-pager
```

Add explicit mounts to a worker when it needs storage not present in the cluster
creation mounts.

```console
dfmicro node add --cluster demo --mount /host/path:/worker/path:ro
```

Use `--force` when debugging worker onboarding and the pre-add readiness check is not desired.

```console
dfmicro node add --cluster demo --force
```

Remove a worker and add another one to reuse the freed slot.

```console
dfmicro node remove --cluster demo --name demo-2
dfmicro node add --cluster demo
kubectl get nodes -o wide
```

## Create an etcd-backed cluster

```console
dfmicro cluster create \
  --name etcd-demo \
  --etcd \
  --no-topolvm \
  --pull-secret ~/pull.json \
  --idms ~/idms.yaml

dfmicro node add --cluster etcd-demo
dfmicro cluster kubeconfig --name etcd-demo >/tmp/etcd-demo-kubeconfig
KUBECONFIG=/tmp/etcd-demo-kubeconfig kubectl get nodes
```

## Peer two clusters

Use distinct pod and service CIDRs when testing peer operations.

```console
dfmicro cluster create \
  --name net-a \
  --no-topolvm \
  --api-server-port 6443 \
  --cluster-cidr 10.42.0.0/16 \
  --service-cidr 10.43.0.0/16 \
  --pull-secret ~/pull.json \
  --idms ~/idms.yaml
dfmicro cluster create \
  --name net-b \
  --no-topolvm \
  --api-server-port 6444 \
  --cluster-cidr 10.52.0.0/16 \
  --service-cidr 10.53.0.0/16 \
  --pull-secret ~/pull.json \
  --idms ~/idms.yaml

dfmicro network peer --cluster net-a --cluster net-b
dfmicro network unpeer --cluster net-a --cluster net-b
```

## Attach clusters to a shared secondary network

Create one Podman bridge and connect both clusters to it. The secondary network uses its own subnet and does not replace the cluster or service networks.

```console
dfmicro network create --name backbone --subnet 172.30.0.0/16
dfmicro network connect --cluster net-a --cluster net-b --to backbone

dfmicro network attach \
  --cluster net-a:group1 \
  --cluster net-b:group1 \
  --to backbone \
  --multi-node
```

With `--multi-node`, the attachment uses Whereabouts IPAM and enables it on the cluster nodes. Whereabouts must already be installed in the clusters.

Deploy a diagnostic pod in each cluster and inspect the assigned secondary addresses.

```console
dfmicro cluster kubeconfig --name net-a >/tmp/net-a-kubeconfig
dfmicro cluster kubeconfig --name net-b >/tmp/net-b-kubeconfig

kubectl --kubeconfig /tmp/net-a-kubeconfig apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: netshoot-a
  annotations:
    k8s.v1.cni.cncf.io/networks: default/backbone-group1
spec:
  containers:
  - name: netshoot
    image: docker.io/nicolaka/netshoot:v0.16
    command: ["sleep", "infinity"]
    securityContext:
      capabilities:
        add: ["NET_RAW"]
EOF

kubectl --kubeconfig /tmp/net-b-kubeconfig apply -f - <<'EOF'
apiVersion: v1
kind: Pod
metadata:
  name: netshoot-b
  annotations:
    k8s.v1.cni.cncf.io/networks: default/backbone-group1
spec:
  containers:
  - name: netshoot
    image: docker.io/nicolaka/netshoot:v0.16
    command: ["sleep", "infinity"]
    securityContext:
      capabilities:
        add: ["NET_RAW"]
EOF

kubectl --kubeconfig /tmp/net-a-kubeconfig get pod netshoot-a -o wide
kubectl --kubeconfig /tmp/net-b-kubeconfig get pod netshoot-b -o wide
```

Inspect and remove the attachment when finished.

```console
kubectl --kubeconfig /tmp/net-a-kubeconfig delete pod netshoot-a --grace-period=10
kubectl --kubeconfig /tmp/net-b-kubeconfig delete pod netshoot-b --grace-period=10
dfmicro network config --name backbone
dfmicro network detach --cluster net-a:group1 --cluster net-b:group1 --from backbone
dfmicro network disconnect --cluster net-a --cluster net-b --from backbone
dfmicro network delete --name backbone
```

## Configure ODF storage topologies

These examples assume the clusters have already been created. Add workers to a
provider before configuring it as a multi-node provider.

Load the host kernel modules before installing ODF operators. The image below
is an example tag. Replace it with an image available to your environment.

```console
dfmicro addon odf modules load
```

### Single-node ODF

Install and configure ODF on an existing single-node cluster:

```console
dfmicro addon odf --cluster standalone install \
  --catalog-image quay.io/example/odf-operator-catalog:v5.0 \
  --channel stable-5.0 --version 5.0.0
dfmicro addon odf --cluster standalone configure

dfmicro cluster kubeconfig --name standalone >/tmp/standalone-kubeconfig
kubectl --kubeconfig /tmp/standalone-kubeconfig get storagecluster -n openshift-storage
kubectl --kubeconfig /tmp/standalone-kubeconfig get storageclass
```

### Rook Ceph provider with an external ODF consumer

Install the Rook operator on `provider` and the ODF operator on `consumer`:

```console
dfmicro addon odf --cluster provider install \
  --catalog-image quay.io/example/odf-operator-catalog:v5.0 \
  --channel stable-5.0 --version 5.0.0 --sub-name rook-ceph-operator
dfmicro addon odf --cluster consumer install \
  --catalog-image quay.io/example/odf-operator-catalog:v5.0 \
  --channel stable-5.0 --version 5.0.0 --sub-name odf-operator
```

Configure Rook on `provider`, then connect `consumer` to it:

```console
dfmicro addon odf --cluster provider configure --external-ceph
dfmicro addon odf --cluster consumer configure --connect-to provider

dfmicro cluster kubeconfig --name provider >/tmp/provider-kubeconfig
dfmicro cluster kubeconfig --name consumer >/tmp/consumer-kubeconfig
kubectl --kubeconfig /tmp/provider-kubeconfig get cephcluster -n openshift-storage
kubectl --kubeconfig /tmp/consumer-kubeconfig get storagecluster -n openshift-storage
```

### ODF provider with a client-only consumer

Install the ODF operator on `provider` and the client operator on `consumer`:

```console
dfmicro addon odf --cluster provider install \
  --catalog-image quay.io/example/odf-operator-catalog:v5.0 \
  --channel stable-5.0 --version 5.0.0 --sub-name odf-operator
dfmicro addon odf --cluster consumer install \
  --catalog-image quay.io/example/odf-operator-catalog:v5.0 \
  --channel stable-5.0 --version 5.0.0 --sub-name ocs-client-operator
```

Configure the provider for host networking and multiple nodes, then onboard the
client-only consumer:

```console
# Host-networked Ceph MONs need separate nodes, so use at least three nodes.
dfmicro addon odf --cluster provider configure --hostnetwork --multi-node
dfmicro addon odf --cluster consumer configure --client --connect-to provider

dfmicro cluster kubeconfig --name provider >/tmp/provider-kubeconfig
dfmicro cluster kubeconfig --name consumer >/tmp/consumer-kubeconfig
kubectl --kubeconfig /tmp/provider-kubeconfig patch storageconsumer consumer \
  -n openshift-storage --type=json \
  -p '[{"op":"add","path":"/spec/storageClasses","value":[{"name":"ocs-storagecluster-ceph-rbd"}]}]'
kubectl --kubeconfig /tmp/consumer-kubeconfig get storageclass
```

Create workloads in `consumer` using the published `ocs-storagecluster-ceph-rbd`
StorageClass to verify client-side provisioning and access.

## Clean up

```console
dfmicro cluster rm --name demo
dfmicro cluster rm --name etcd-demo
dfmicro cluster rm --name net-a
dfmicro cluster rm --name net-b
```
