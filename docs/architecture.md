# architecture

## Pieces

```
        ┌──────────┐
 user ──► cobra cli ├──► config / target / selector
        └────┬─────┘
             ▼
        ┌──────────┐    ┌──────────────┐
        │ provider │ ──►│ cloud / virt │  up·down·budget·orphans
        └────┬─────┘    └──────────────┘  (parallels, tart, hetzner,
             │  ready Target                 aws, azure, gcp, windows)
             ▼
        ┌──────────┐
        │  fleet   │  ── prefixed mux ──► stdout/stderr
        └────┬─────┘
             ▼
        ┌──────────┐    ┌──────────────┐
        │ transport│ ──►│ external CLI │  (crabbox, ssh, ssh-windows,
        └────┬─────┘    └──────────────┘   parallels-guest, abx, guiport,
             ▼                              adb, idb, simctl, maestro)
        ┌──────────┐
        │ evidence │  ── ~/.vmlab/runs/<id>/{meta,target/*}
        └──────────┘
```

Providers are optional: a `Target` that points at an already-running host
skips the provider layer entirely and goes straight to a transport.

## Concepts

- **Target.** `(name, transport, tags, transport-specific settings)`. YAML files
  layered user → repo. See `internal/target/`.
- **Provider.** Owns instance *lifecycle* — `Up` scales a VM up and returns a
  ready `Target`, `Down` scales it back down per its disposition. Optional
  `Priced`/`Snapshotter` capabilities add budget caps and snapshots. See
  `internal/provider/` and [`providers.md`](providers.md).
- **Transport.** Interface in `internal/transport/transport.go`. Adapters shell
  to external CLIs; no SDK reimplementation.
- **Selector.** Tag-aware expression resolved against a `Registry`. Operators:
  `@tag`, `,` (AND), `;` (union), `not:@tag`, `all`, exact name.
- **Flow.** YAML with `run` and `assert` steps. Anything more goes in your shell
  scripts.
- **Fleet.** Concurrent runner over targets with prefixed mux, fail-fast,
  continue-on-error, and aggregated exit code.
- **Evidence.** One dir per run — meta.json + per-target stdout/stderr/steps.

## Adding a transport

1. Create `internal/transport/<name>.go`.
2. Implement `Name`, `Capabilities`, `Doctor`, `Run`, `Sync`, `Shell`,
   `Screenshot`, `GUI`. Use the helpers in `exec.go`.
3. Register in `transport.Default()`.
4. Add a docs page under `docs/transports/<name>.md`.

Target the helpers, not raw `exec.Cmd` — they handle exit codes, missing
binaries, and signal propagation consistently.
