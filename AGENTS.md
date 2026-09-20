# Agent Guidelines

dfmicro runs MicroShift clusters in rootful Podman containers. Clusters can have a control node and workers. CLI built on urfave/cli v3.

## Packages

Every command registered in internal/app/app.go comes from L1 of internal/ and only internal/addon/ has L2 containing code.

There'll only ever be a single subcommand, ex: `dfmicro cmd subcmd --flags` except for the `addon` command, dir under addon is a separate island for managing different addons on top of base cluster.

```
> tree internal/ -L1
internal/
├── addon
├── app
├── buildinfo
├── cluster
├── config
├── devlog
├── docs
├── execx
├── lore
├── network
├── ops
└── support
```

Some examples:
- `execx`: all process execution. Never shell out directly, always use `execx.Runner`.
- `support`: shared utilities only. No domain logic.
- `cluster`, `network`, `addon`, `lore`: domain packages, each owns its full subdomain. `lore` is used by the separate `cmd/fetch` binary.
- `internal/config`: shared configuration types and defaults via `sync.OnceValue`. Never hardcode values in the flag defaults and source them from here.

## Conventions

- Structs with methods over free functions. Lowercase unless another package needs it.
- Commands are thin: parse flags, call manager, return error. No logic in action closures.
- Validate at CLI flag layer (`Validator`), not in business logic.
- Comments only when the WHY is non-obvious. Never what/how.

## Build and verify

```
make build        # fmt + vet + generate + compile
```

Run `make generate` after changing any `Usage`, `UsageText`, or flag definitions. Ensure `Usage` and `UsageText` is brief as it forms the documentation of the CLI.

Secondary network attachment uses host-local IPAM by default. `network attach --multi-node` selects Whereabouts and labels current and future cluster nodes.

Cluster settings are stored in the cluster directory. Control-node settings are part of the cluster config, worker settings are stored in `nodes.json`, and node state directories are derived from the cluster and node names.

## Etiquette

- Never bring external dependencies and prefer using stdlib as bloating the binary is unacceptable, you can occasionally check the binary compiled by `make build-analyze` with `gsa`. Anecdote: ./internal/support/http.go is build gated because dfmicro binary doesn't use http.
- All packages only export `Command` function and everything else is private, the types, funcs, methods etc. Fields are only exported if they are Marshaled/Unmarshaled.

## References

Longer form references are at ./README.md and ./dev.md, reading them is not mandatory. A changelog is maintained at ./internal/devlog/devlog.txt with single line items.
