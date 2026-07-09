# dfmicro

Run single-node [MicroShift](https://github.com/openshift/microshift) clusters inside rootful Podman containers.
Each cluster gets its own network, loop-device backed LVM storage, and a kubeconfig.

Verified on Linux (Fedora / RHEL). Best-effort support on macOS via rootful Podman machine.

## Installation

Download the latest release for your platform from the [releases page](https://github.com/leelavg/dfmicro/releases),
extract, and place the binary on your PATH.

```
tar -xzf dfmicro_linux_amd64.tar.gz
install -m 0755 dfmicro ~/.local/bin/dfmicro
```

Or build from source:

```
git clone -b main https://github.com/leelavg/dfmicro
cd dfmicro
make build
```

## Quick start

```
dfmicro ops sudoers create          # one-time: passwordless sudo for cluster tools
dfmicro cluster create              # create cluster with default name
dfmicro cluster kubeconfig > ~/.kube/config
kubectl get nodes
```

## Command reference

See [internal/docs/cli.md](internal/docs/cli.md) for the full command reference,
or run `dfmicro docs` to print it.

For development setup and design notes, see [dev.md](dev.md). For what is planned and what is done, see the [devlog](internal/devlog/DEVLOG.txt).

Bug reports and suggestions are welcome via issues. This is a personal project with a focused scope for now, so pull requests are not being accepted at this time.

## Acknowledgements

None of this would exist without the incredible work at [MicroShift](https://github.com/microshift-io/microshift).

Thanks to [Anika](https://github.com/AnikaYadav) and [Tara](https://github.com/taraasrita10) for sourcing the shims and carrying out the proof of concept under guidance.

Thanks to all the folks whose shared knowledge archived in IBM Bob and Claude Code agents made the timeline shorter.

## License

Apache 2.0. See [LICENSE](LICENSE).
