# NAME

dfmicro - Manage dfmicro clusters

# SYNOPSIS

dfmicro

**Usage**:

```
dfmicro [GLOBAL OPTIONS] [command [COMMAND OPTIONS]] [ARGUMENTS...]
```

# COMMANDS

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

**--no-expose-kubeapi**: Disable exposing the Kubernetes API server on the host

**--no-share-host-containers**: Disable mounting host /var/lib/containers for image reuse

**--overprovision-ratio**="": TopoLVM thin pool overprovision ratio (default: 10)

**--pull-secret**="": Path to pull secret file

### delete, rm

Delete cluster containers and storage

**--name**="": Cluster name (default: "cluster")

### exec

Execute a shell in a running container

**--container**="": Container name (defaults to first running container)

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

## perms

Manage sudo permissions for dfmicro (Linux only)

### create

Create sudoers configuration for passwordless sudo

### delete

Remove sudoers configuration
