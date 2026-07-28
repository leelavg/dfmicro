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

## FAQ

<details>
<summary>How do I access cluster routes from my host?</summary>
<br>
IPs and Domain names mentioned are for default values, edit as per your cluster settings.

Routes like `metrics.apps.example.com` need DNS resolution to the cluster node IP. Find your cluster's node IP:

```bash
kubectl get nodes -o wide | grep 172.20
```

Choose your setup method (replace `172.20.0.11` with your actual node IP):

**Linux with NetworkManager + dnsmasq:**
```bash
echo 'address=/apps.example.com/172.20.0.11' | sudo tee /etc/NetworkManager/dnsmasq.d/dfmicro.conf

sudo nmcli general reload dns-full
curl -k https://metrics.apps.example.com/metrics
```

**Linux with standalone dnsmasq:**
```bash
echo 'address=/apps.example.com/172.20.0.11' | sudo tee /etc/dnsmasq.d/dfmicro.conf

sudo systemctl reload dnsmasq
curl -k https://metrics.apps.example.com/metrics
```

**Linux with systemd-resolved:**
```bash
echo 'DNS=172.20.0.11' | sudo tee /etc/systemd/resolved.conf.d/dfmicro.conf
echo 'Domains=~apps.example.com' | sudo tee -a /etc/systemd/resolved.conf.d/dfmicro.conf
sudo systemctl restart systemd-resolved
curl -k https://metrics.apps.example.com/metrics
```

**Linux / macOS:**
```bash
echo '172.20.0.11 metrics.apps.example.com' | sudo tee -a /etc/hosts
curl -k https://metrics.apps.example.com/metrics
```

Or use `curl --resolve` (no `/etc/hosts` edit):
```bash
curl -k --resolve metrics.apps.example.com:443:172.20.0.11 https://metrics.apps.example.com/metrics
```

**Cleanup:**
```bash
sudo rm -f /etc/NetworkManager/dnsmasq.d/dfmicro.conf /etc/dnsmasq.d/dfmicro.conf
sudo nmcli general reload dns-full || sudo systemctl reload dnsmasq
```
</details>

<details>
<summary>Why dnsmasq instead of in-cluster DNS?</summary>
<br>
Podman's aardvark-dns only resolves container names, not in-cluster routes. It has no upstream config to forward to CoreDNS. The router on node IP `172.20.0.11` handles SNI dispatch, so dnsmasq just needs to answer `*.apps.example.com` with that IP.
</details>

## Contributing

Bug reports and suggestions are welcome via [issues](https://github.com/leelavg/dfmicro/issues). This is a personal project with a focused scope for now, so pull requests are not being accepted at this time.

## Acknowledgements

None of this would exist without the incredible work at [MicroShift](https://github.com/microshift-io/microshift).

Thanks to [Anika](https://github.com/AnikaYadav) and [Tara](https://github.com/taraasrita10) for sourcing the shims and carrying out the proof of concept under guidance.

Thanks to all the folks whose shared knowledge archived in IBM Bob and Claude Code agents made the timeline shorter.

## License

Apache 2.0. See [LICENSE](LICENSE).
