# architecture

## Pieces

```
        ┌──────────┐
 user ──► cobra cli ├──► config / target / selector
        └────┬─────┘
             ▼
        ┌──────────┐
        │  fleet   │  ── prefixed mux ──► stdout/stderr
        └────┬─────┘
             ▼
        ┌──────────┐    ┌──────────────┐
        │ transport│ ──►│ external CLI │  (crabbox, abx, guiport, adb,
        └────┬─────┘    └──────────────┘   idb, simctl, maestro, sh)
             ▼
        ┌──────────┐
        │ evidence │  ── ~/.vmlab/runs/<id>/{meta,target/*}
        └──────────┘
```

## Concepts

- **Target.** `(name, transport, tags, transport-specific settings)`. YAML files
  layered user → repo. See `internal/target/`.
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
