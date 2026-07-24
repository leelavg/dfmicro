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
  dfmicro ops sudoers create                   # one-time: passwordless sudo for cluster tools
  dfmicro cluster create                       # create cluster with default name
  dfmicro cluster kubeconfig > ~/.kube/config  # overwrites kubeconfig!
  kubectl get nodes
  dfmicro cluster delete                       # tear everything down

**Usage**:

```
dfmicro [GLOBAL OPTIONS] [command [COMMAND OPTIONS]] [ARGUMENTS...]
```

# COMMANDS

## addon

Manage cluster addons

**--list**: List available addons

### odf

Manage OpenShift Data Foundation on a MicroShift cluster

    Manage ODF lifecycle on MicroShift. Verified on Linux, not tested on macOS.
    
    Note: --name and --kubeconfig apply to all subcommands and must come before the subcommand name.

**--kubeconfig**="": Path to an existing kubeconfig file

**--kubectl**: Use kubectl instead of oc for cluster operations

**--name**="": Cluster name to resolve kubeconfig from (default: "micro")

#### configure

Configure ODF to run on MicroShift in an opinionated single-node setup

>Run after 'install' once the operator CSV reaches Succeeded. Applies without retries and fails fast on any error.

#### install

Install ODF and required shim resources

    Requires rbd, ceph, nbd kernel modules loaded on the host. Run 'dfmicro addon odf modules load' first.
    
    Example:
      dfmicro addon odf install --catalog-image quay.io/example/catalog:v4.16 --channel stable-4.16 --version 4.16.0

**--catalog-image**="": Catalog source image

**--channel**="": Subscription channel (e.g. stable-4.16)

**--sub-name**="": Subscription name (repeatable) (default: "odf-operator")

**--version**="": OCP version in X.Y.Z format (e.g. 4.16.0)

#### modules

Manage ODF kernel module auto-load configuration

##### load

Load rbd, ceph, nbd kernel modules and configure auto-load at boot

##### unload

Unload rbd, ceph, nbd kernel modules and remove auto-load config

#### uninstall

Uninstall ODF and all associated resources

    Prints the cleanup commands by default. Pass --attempt to execute them (best-effort).
    
    Examples:
      dfmicro addon odf uninstall            # dry-run: print commands
      dfmicro addon odf uninstall --attempt  # execute cleanup

**--attempt**: Execute the delete commands instead of printing them (best-effort)

## cluster

Manage cluster lifecycle

>Manage MicroShift cluster lifecycle in rootful Podman containers.

### config

Print saved cluster config as JSON

>Config is recorded at creation time and reflects the flags used.

**--name**="": Cluster name (default: "micro")

### create

Create a cluster, wait until ready, and print connection info

    Mounts flags are immutable after creation. Delete and recreate to change them.
    
    Examples:
      dfmicro cluster create
      dfmicro cluster create --name dev --network-subnet 10.88.0.0/24
      dfmicro cluster create --name odf --lvm-volsize 50G --pull-secret ~/pull-secret.json
      dfmicro cluster create --idms ~/idms-1.yaml --idms ~/idms-2.yaml

**--api-server-port**="": Host port to expose the Kubernetes API server on (1024-65535) (default: 6443)

**--idms**="": Path to an ImageDigestMirrorSet YAML file for mirror registries (repeatable, merged in order)

**--image**="": MicroShift container image to run (OKD / SCOS build) (default: "ghcr.io/leelavg/microshift:5.0.0_202607050937_g45630c7b1_5.0.0_okd_scos.ec.4")

**--lvm-volsize**="": Size of the sparse loop-device image backing the LVM thin pool for TopoLVM (e.g. 10G, 50G) (default: "10G")

**--mount**="": Extra bind mount in Podman format: /host/path:/container/path[:opts] (repeatable)

**--name**="": Cluster name, used to identify containers and stored config (default: "micro")

**--network-subnet**="": IPv4 private CIDR for the Podman network (RFC 1918 only) (default: "172.20.0.0/24")

**--no-expose-kubeapi**: Do not bind the API server port on the host (cluster-internal access only)

**--no-power-tuning**: Do not apply MicroShift power tuning on create

**--no-share-host-containers**: Do not bind-mount /var/lib/containers from the host (use if the shared containers store gets corrupted)

**--no-thinpool**: Skip thin pool creation and configuration for TopoLVM storage

**--overprovision-ratio**="": TopoLVM thin pool overprovision ratio (default: 20)

**--pull-secret**="": Path to a pull secret JSON file for accessing private image registries

### delete, rm

Delete cluster containers, network, and storage

>Stops and removes all cluster containers, networking, and storage stack.

**--name**="": Cluster name (default: "micro")

### exec

Open an interactive shell inside the cluster container

>Useful for running crictl, oc, or kubectl directly against the node.

**--container**="": Container name (defaults to first running container for the cluster)

**--name**="": Cluster name (default: "micro")

### kubeconfig

Print kubeconfig for a cluster

    Pipe to a file or merge into an existing kubeconfig:
    
      dfmicro cluster kubeconfig > ~/.kube/config
      dfmicro cluster kubeconfig | KUBECONFIG=~/.kube/config:- kubectl config view --merge --flatten > merged.yaml

**--name**="": Cluster name (default: "micro")

### list, ls

List all clusters

### start

Start a stopped cluster

>Use after 'cluster stop' or after a host reboot.

**--name**="": Cluster name (default: "micro")

### stop

Stop cluster containers without removing them

>Preserves all state. Resume with 'cluster start'.

**--name**="": Cluster name (default: "micro")

## config

Print the embedded default configuration as JSON

    Shows the compiled-in defaults for cluster name, image, network subnet, LVM size, and more.
    These are the values used when flags are omitted on any command.

## docs

Print full command reference as markdown

>dfmicro docs > cli.md

## ops

Operational utilities for running clusters

### resources

Show CPU and memory requests, limits, and live usage per container (experimental)

    Experimental: output format and flags may change. Use --namespace to scope and improve performance.
    
    Examples:
      dfmicro ops resources
      dfmicro ops resources --namespace openshift-operator-lifecycle-manager
      dfmicro ops resources --name dev --node microshift-node-1

**--name**="": Cluster name (default: "micro")

**--namespace**="": Restrict output to a single namespace (omit for all namespaces)

**--node**="": Restrict output to a single node by name (omit for all nodes)

### sudoers

Manage passwordless sudo configuration for dfmicro (Linux only)

    Writes /etc/sudoers.d/dfmicro with the commands used by dfmicro requiring elevated access.
    
    No-op on macOS: rootful Podman machine runs as root so no sudoers entry is needed.
    
    Warning: these rules allow any process running as your user to invoke the listed binaries without a password prompt. Intended for developer workstations, not shared hosts.

#### create

Write /etc/sudoers.d/dfmicro for the current user

#### delete

Remove /etc/sudoers.d/dfmicro

>Removes the sudoers file created by 'sudoers create'. On macOS this is a no-op.
