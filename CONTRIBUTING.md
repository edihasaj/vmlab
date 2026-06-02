# Contributing to vmlab

Thanks for taking a look. vmlab is small and opinionated — contributions that keep it that way are welcome.

## Wedge

vmlab is a **transport-agnostic orchestrator for cross-platform verify loops** — it composes existing tools (crabbox/abx/guiport/adb/idb/maestro) rather than replacing them. Keep PRs aligned with that wedge:

- Tighter transport adapters and more reliable doctor checks.
- Better provider lifecycle (up/down/snapshot/orphans) without leaking cost or state.
- Cleaner agent surfaces (CLI flags, MCP tools, evidence bundles).
- New transports/providers — but only as ~200 LOC adapters behind the existing interfaces, never forks of the tools they drive.

If a feature reimplements something crabbox/abx/guiport/adb/idb/maestro already does, it's out of scope.

## Requirements

- Go ≥1.24
- The vendor CLIs for whatever transports/providers you touch (e.g. `crabbox`, `abx`, `guiport`, `adb`, `idb`, `xcrun simctl`, `maestro`, `prlctl`, `hcloud`, `aws`, `az`, `gcloud`). `vmlab doctor` tells you what's missing.

## Build & test

```sh
make build            # → ./bin/vmlab
make install          # → $GOPATH/bin/vmlab (or PREFIX=$HOME/.local make install)
go build ./...
go test ./...
vmlab doctor          # check transport/provider binaries on PATH
```

## PR checklist

- [ ] `go build ./...` clean.
- [ ] `go test ./...` passes.
- [ ] `gofmt -l .` is empty (run `gofmt -w` on touched files).
- [ ] If you changed behavior, update `README.md` / `docs/` / `examples/`.
- [ ] If you changed CLI surface, update the README command table + quick-start.
- [ ] Conventional commit subject (`feat:`, `fix:`, `refactor:`, `docs:`, `test:`, `chore:`, `perf:`).
- [ ] No `--no-verify`, no `--amend` of pushed commits.

## Style

- Telegraph-style commit bodies — say *what* and *why*, skip filler.
- Keep files under ~500 LOC; split packages instead of monoliths.
- No comments restating the code; only document non-obvious *why*.
- Tests next to the code they cover (`internal/<pkg>/*_test.go`).
- Don't hardcode hosts, paths, or secrets in examples — credentials flow through env / `op://` references.

## Reporting bugs

Use the issue templates. Include:

- OS + arch (`uname -sm`)
- `vmlab version`
- The transport/provider involved
- `vmlab doctor` output (`--json` preferred)
- Exact repro commands (and the flow YAML if applicable)

## Security

See [`SECURITY.md`](SECURITY.md). Don't open public issues for security reports.

## License

MIT. By contributing you agree your contributions are licensed under MIT.
