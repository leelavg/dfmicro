# Examples

## Prepare sudo access

Create the sudoers rules once. Run dfmicro itself without sudo.

```console
dfmicro ops sudoers create
```

## Create a cluster

Create the default SQLite-backed cluster and inspect its node.

```console
dfmicro cluster create --name test
sudo podman exec test-1 kubectl get nodes
```

## Create an etcd-backed cluster without TopoLVM

Use this for testing cluster and worker behavior without creating LVM storage.

```console
dfmicro cluster create --name test --etcd --no-topolvm
sudo podman exec test-1 kubectl get nodes
```

## Add and inspect a worker

Add a worker after the control plane is ready. The command waits for the control plane before starting the worker.

```console
dfmicro node add --cluster test
sudo podman exec test-1 kubectl get nodes
sudo podman exec test-1 kubectl get pods -A
sudo podman exec test-2 journalctl -u microshift --no-pager
```

For debugging, skip only the pre-add control-plane readiness checks.

```console
dfmicro node add --cluster test --force
```

## Remove a worker

Remove a worker and free its node-specific state and storage.

```console
dfmicro node remove --cluster test --name test-2
```

## Create a cluster with registry configuration

Pass the pull secret and IDMS files to propagate registry configuration to every node.

```console
dfmicro cluster create \
  --name test \
  --pull-secret ~/pull.json \
  --idms ~/idms.yaml
```

## Remove the cluster

Remove all containers, network state, node state, and storage created for the cluster.

```console
dfmicro cluster remove --name test
```
