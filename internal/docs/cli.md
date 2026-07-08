# NAME

dfmicro - Manage dfmicro clusters

# SYNOPSIS

dfmicro

**Usage**:

```
dfmicro [GLOBAL OPTIONS] [command [COMMAND OPTIONS]] [ARGUMENTS...]
```

# COMMANDS

## addon

Manage cluster addons

### odf

Manage OpenShift Data Foundation

>dfmicro addon odf [options] <command>  # options must precede the subcommand

**--kubeconfig**="": Path to kubeconfig file

**--kubectl**: Use kubectl instead of oc

**--name**="": Cluster name to resolve kubeconfig from (default: "cluster")

#### configure

Deploy StorageCluster after operator is ready

#### install

Install ODF operator and shim resources

**--catalog-image**="": Catalog source image

**--channel**="": Subscription channel

**--sub-name**="": Subscription name (default: "odf-operator")

**--version**="": OCP version (e.g. 4.16.0)

#### modules

Manage ODF kernel module auto-load configuration

##### load

Configure rbd, ceph, nbd modules to load at boot

##### unload

Remove ODF kernel module auto-load configuration

#### uninstall

Uninstall ODF (prints commands by default)

**--attempt**: Actually run the delete commands (best-effort)

## cluster

Manage cluster lifecycle

### config

Print saved cluster config

**--name**="": Cluster name

### create

Create a cluster, wait until ready, and write kubeconfig

**--api-server-port**="": Host port to expose the Kubernetes API server on (default: 6443)

**--idms**="": Path(s) to ImageDigestMirrorSet yaml files for mirror registries, merged in order

**--image**="": Container image to run for cluster nodes (default: "ghcr.io/leelavg/microshift:5.0.0_202607050937_g45630c7b1_5.0.0_okd_scos.ec.4")

**--lvm-volsize**="": Size of the sparse disk image used for TopoLVM (default: "10G")

**--mount**="": Extra volume mounts in podman format: /host/path:/container/path[:opts]

**--name**="": Cluster name (default: "cluster")

**--network-subnet**="": Subnet for the podman network in CIDR notation (default: "172.20.0.0/24")

**--no-expose-kubeapi**: Disable exposing the Kubernetes API server on the host

**--no-share-host-containers**: Disable mounting host /var/lib/containers for image reuse

**--overprovision-ratio**="": TopoLVM thin pool overprovision ratio (default: 20)

**--pull-secret**="": Path to pull secret file

### delete, rm

Delete cluster containers and storage

**--name**="": Cluster name (default: "cluster")

### exec

Execute a shell in a running container

**--container**="": Container name (defaults to first running container)

**--name**="": Cluster name (default: "cluster")

### kubeconfig

Print kubeconfig for a cluster

**--name**="": Cluster name (default: "cluster")

### list, ls

List all dfmicro clusters

### start

Start cluster containers

**--name**="": Cluster name (default: "cluster")

### stop

Stop cluster containers

**--name**="": Cluster name (default: "cluster")

## config

Print top-level embedded config

## docs

Print full command reference as markdown

## ops

Operational utilities for running clusters

### resources

Show CPU and memory requests, limits, and usage per container (experimental)

**--name**="": Cluster name (default: "cluster")

**--namespace**="": Namespace to inspect (omit for all namespaces)

**--node**="": Node name to inspect (omit for all nodes)

### sudoers

Manage sudo permissions for dfmicro (Linux only)

#### create

Create sudoers configuration for passwordless sudo

#### delete

Remove sudoers configuration
