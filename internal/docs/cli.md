# NAME

dfmicro - Run MicroShift clusters in rootful Podman containers

# SYNOPSIS

dfmicro

# DESCRIPTION

dfmicro creates and manages single-node MicroShift clusters inside rootful Podman containers.
Each cluster gets its own Podman network and a loop-device backed LVM thin pool for TopoLVM storage.

Verified on: Linux (Fedora / RHEL)
Best-effort support: macOS (requires rootful Podman machine via 'podman machine init --rootful')

Quick start:
  dfmicro ops sudoers create                              # one-time: passwordless sudo for cluster tools
  dfmicro cluster create                                  # create cluster named 'cluster'
  dfmicro cluster kubeconfig > ~/.kube/config             # export kubeconfig
  kubectl get nodes                                       # verify node is Ready
  dfmicro cluster delete                                  # tear everything down

**Usage**:

```
dfmicro [GLOBAL OPTIONS] [command [COMMAND OPTIONS]] [ARGUMENTS...]
```

# COMMANDS

## addon

Manage cluster addons

### odf

Manage OpenShift Data Foundation on a MicroShift cluster

>dfmicro addon odf [--name NAME | --kubeconfig PATH] [--kubectl] <command>

**--kubeconfig**="": Path to an existing kubeconfig file

**--kubectl**: Use kubectl instead of oc for cluster operations

**--name**="": Cluster name to resolve kubeconfig from (default: "cluster")

#### configure

Apply SINGLE_NODE StorageCluster CR and label the storage node

#### install

Install ODF operator, shim CRDs, RBAC, and catalog source

**--catalog-image**="": Catalog source image containing the ODF operator bundle

**--channel**="": OLM subscription channel (e.g. stable-4.16)

**--sub-name**="": OLM subscription name(s) to create (repeatable) (default: "odf-operator")

**--version**="": OCP version string in X.Y.Z format (e.g. 4.16.0) used to select the correct shim resources

#### modules

Manage ODF kernel module auto-load configuration

##### load

Write modules-load.d config and load rbd, ceph, nbd for the current session

##### unload

Remove modules-load.d config and unload rbd, ceph, nbd from the current session

#### uninstall

Uninstall ODF operator and all associated resources

**--attempt**: Execute the delete commands instead of printing them (best-effort)

## cluster

Manage cluster lifecycle

### config

Print saved cluster config as JSON

**--name**="": Cluster name

### create

Create a cluster, wait until ready, and print connection info

**--api-server-port**="": Host port to expose the Kubernetes API server on (1024-65535) (default: 6443)

**--idms**="": Path(s) to ImageDigestMirrorSet YAML files for mirror registries; merged in order given

**--image**="": MicroShift container image to run (OKD / SCOS build) (default: "ghcr.io/leelavg/microshift:5.0.0_202607050937_g45630c7b1_5.0.0_okd_scos.ec.4")

**--lvm-volsize**="": Size of the sparse loop-device image backing the LVM thin pool for TopoLVM (e.g. 10G, 50G) (default: "10G")

**--mount**="": Extra bind mounts in Podman format: /host/path:/container/path[:opts] (repeatable)

**--name**="": Cluster name, used to identify containers and stored config (default: "cluster")

**--network-subnet**="": IPv4 private CIDR for the dedicated Podman network (RFC 1918 only, e.g. 10.88.0.0/24) (default: "172.20.0.0/24")

**--no-expose-kubeapi**: Do not bind the API server port on the host (cluster-internal access only)

**--no-share-host-containers**: Do not bind-mount /var/lib/containers from the host (disables image layer reuse, slower pulls)

**--overprovision-ratio**="": TopoLVM thin pool overprovision ratio; total allocatable storage = volsize * ratio (default: 20)

**--pull-secret**="": Path to a Red Hat pull secret JSON file (required for registries.redhat.io images)

### delete, rm

Delete cluster containers, network, and storage

**--name**="": Cluster name (default: "cluster")

### exec

Open an interactive shell inside the cluster container

**--container**="": Container name (defaults to first running container for the cluster)

**--name**="": Cluster name (default: "cluster")

### kubeconfig

Print kubeconfig for a cluster

**--name**="": Cluster name (default: "cluster")

### list, ls

List all dfmicro clusters

### start

Start a stopped cluster

**--name**="": Cluster name (default: "cluster")

### stop

Stop cluster containers without removing them

**--name**="": Cluster name (default: "cluster")

## config

Print the embedded default configuration as JSON

## docs

Print full command reference as markdown

>dfmicro docs > cli.md

## ops

Operational utilities for running clusters

### resources

Show CPU and memory requests, limits, and live usage per container (experimental)

**--name**="": Cluster name (default: "cluster")

**--namespace**="": Restrict output to a single namespace (omit for all namespaces)

**--node**="": Restrict output to a single node by name (omit for all nodes)

### sudoers

Manage passwordless sudo configuration for dfmicro (Linux only)

#### create

Write /etc/sudoers.d/dfmicro for the current user

#### delete

Remove /etc/sudoers.d/dfmicro
