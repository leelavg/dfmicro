# Development

## Prerequisites

- Go 1.26+
- rootful Podman (Linux) or rootful Podman machine (macOS)
- `sudo` access, or run `dfmicro ops sudoers create` once

## Build

```
make build          # fmt, vet, generate, compile -> bin/dfmicro
make build-release  # same with -trimpath -s -w (stripped)
make build-analyze  # same with -trimpath only (readable by gsa)
```

## Generated files

`internal/docs/cli.md` is generated from the command definitions. Regenerate after changing any `Usage`, `UsageText`, or flag definitions:

```
make generate
```

The CI workflow checks that committed docs match the source.

## Design decisions

**Command structure**

Commands are grouped into three top-level domains: `cluster` (lifecycle), `addon` (optional workloads), and `ops` (host utilities). Each domain lives in its own package under `internal/`. The root command sorts all subcommands and flags alphabetically at startup via `support.SortCommand()` so the help output is stable without manual ordering.

**Addon mechanism**

An addon is a package under `internal/addon/<name>/` that exposes a `*cli.Command`. It manages its own install, configure, and uninstall lifecycle independently of the cluster package. Addons talk to the cluster through `oc` or `kubectl` (resolved at runtime via `--kubectl` flag), with the kubeconfig sourced either from a cluster name lookup or a direct path. Shim resources (CRDs, RBAC, catalog sources) are numbered and embedded so they apply in a deterministic order without depending on server-side ordering. The `--attempt` flag on destructive operations defaults to dry-run so the user can review what will be deleted before anything runs.

**Why podman exec only**

All cluster operations (kubectl, crictl, oc) go through `podman exec` into the running cluster container. This sidesteps the need to route the API server through the host network or manage certificates for host-side client-go connections. The tradeoff is one process spawn per call and no streaming. Replacing this with the Podman socket API and client-go is tracked in the devlog.

## Release

Releases are cut by pushing a version tag. GoReleaser builds cross-platform binaries, signs checksums with cosign, and publishes to GitHub Releases.

```
git tag v0.x.0
git push origin v0.x.0
```

## Code Standards

**Comments only when required.** Only add `// comment` when the WHY is non-obvious: a hidden constraint, subtle invariant, workaround for a specific bug, or behavior that would surprise a reader. No sprinkled inline documentation, no refactoring comments, no references to issues/PRs.

**Structs and methods, not standalone functions.** Prefer types with methods hanging off them over free functions. Cleaner state management, less parameter threading. Export only what external packages need.

**Minimal exports.** Lowercase for internals. Private to the package unless another package must use it.

**Defaults from build-time config.** Read defaults from embedded JSON (via `sync.OnceValue`), not hardcoded. Single source of truth. Example: `internal/config/defaults.json` loaded once in `config.Load()`.

**Validation at CLI layer.** Use urfave Validator on flags, not error returns in business logic. Let the framework reject invalid input before calling handler code.

**Review comments during development.** Use `// -` inline comments to mark areas for refactoring or decisions pending feedback. Example: `// - move to support/log.go and reuse`. These are addressed before merge, not shipped.
