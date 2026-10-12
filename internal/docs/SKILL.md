---
name: dfmicro
description: Operate a user's local MicroShift clusters with dfmicro when asked to manage clusters, nodes, networks, or addons.
---

# Operating dfmicro

dfmicro runs MicroShift clusters as rootful Podman containers. A cluster starts with a control node and can be extended with workers. Cluster, node, network, and addon operations are separate and composable.

## Host requirements

- Linux: rootful Podman, sudo access, LVM/device-mapper tools (`vgcreate`, `lvcreate`, `dmsetup`), and loop-device tools (`losetup`). Choose a privileged-operation method described below.
- macOS: a rootful Podman machine, initialized with `podman machine init --rootful`. Linux-specific privileged operations run inside the VM. macOS support is best-effort.

## Addons

- ODF use additionally requires the host kernel modules loaded with `dfmicro addon odf modules load` and idms/pull-secret mounts during cluster creation for private catalogs.

## Operating pattern

Before changing anything, inspect the current state with `dfmicro cluster ls`, `dfmicro cluster config`, and the relevant `dfmicro node config` or `dfmicro network config` commands if node/network exists. Check the command help when options or defaults matter.

Create a cluster with `dfmicro cluster create`, then add workers with `dfmicro node add` as needed. Networking and addons are separate operations layered onto existing clusters: create and connect Podman networks with `dfmicro network`, configure pod networking with `dfmicro network attach`, and use peer operations when cluster-CIDR routing between clusters is needed. Install, configure, and uninstall addons independently with `dfmicro addon <name>`.

Container bind mounts are fixed when a node container is created. Restarting does not change them. Control-node mounts are selected at cluster creation. Worker-specific mounts can be supplied to `dfmicro node add`. Changing a worker's mounts requires removing and re-adding that worker.

Treat existing clusters, networks, volumes, and host resources as user data. Do not remove or recreate them unless the user requested it. Choose one of the privileged-operation methods below. After a change, verify the affected cluster or resource and report any failures rather than assuming readiness.

## Privileged operations

Users can choose how dfmicro runs privileged commands:
1. Run `dfmicro ops sudoers create` to add dfmicro's required commands to sudoers so they can run without prompting.
2. Run `sudo dfmicro`. Commands in that invocation run with elevated privileges.
3. Run `dfmicro` normally. It prompts for sudo credentials when a privileged command is needed.

## Built-in references

- `dfmicro docs`: generated CLI reference
- `dfmicro docs --examples`: scenario-based commands
- `dfmicro docs --skill`: this guide
- `dfmicro docs --devlog`: hidden development notes
- `dfmicro config`: embedded defaults
