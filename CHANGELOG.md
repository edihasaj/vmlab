# Changelog

All notable changes to vmlab will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added (v0.1.1 candidate — full system control)

- Instance `mounts:` block — host-to-guest file shares declared per instance.
  Parallels: auto-configured as shared folders on `vmlab up` (visible as
  `\\Mac\<name>` in Windows guests). SSH: rsync'd by `vmlab sync`.
  Idempotent across re-Up.
- `vmlab sync <instance>` — explicit sync command. For Parallels it
  ensures shared folders are wired; for SSH it rsyncs each mount's host
  path into the guest. `--path` overrides instance mounts:.
- Snapshots: new `Snapshotter` capability interface; Parallels impl wraps
  `prlctl snapshot/snapshot-list/snapshot-switch/snapshot-delete`.
  CLI: `vmlab snapshot save/restore/ls/rm`. JSON output supported.
- `vmlab wait <instance>` — re-poll provider readiness (Parallels Tools /
  TCP:22) after a guest reboot, without re-doing `Up`. Backed by a new
  `ReadyWaiter` optional interface; both Parallels and Hetzner expose it.
- SSH transport `Sync` now uses rsync when available (incremental, much
  faster than scp), falling back to scp on hosts without rsync.
- Tests: parseSnapshotList (real prlctl --json output), ensureMounts
  idempotency, snapshot ID lookup, parallels-guest Sync configures the
  shared folder via `--shf-host-add`.

**Live-verified on edis-mac-studio Win11:** mount config persists across
Up cycles; guest read+write through `\\Mac\smoke`; snapshot save → ls →
restore → rm; `vmlab wait` returns immediately when tools are up.

**Known constraint:** `mounts.host` paths are resolved on the Parallels
host (the Mac running Parallels Desktop), not on the laptop running
vmlab. Cross-machine sync to the Parallels host is out of scope; if
needed, rsync to that host first.


### Added (providers — v0.1.0 candidate)

- Provider abstraction (`internal/provider`): lifecycle (`Status` / `Up` /
  `Down`) on top of the existing transports, idempotent by design,
  `EnsureResult.Changed` so cleanup only fires for state vmlab caused.
- Instance config (`~/.vmlab/instances/<name>.yaml`, repo overrides) with
  JSON-schema validation; `ready`, `target`, `disposition` blocks.
- CLI: `vmlab provider ls/doctor`, `vmlab instance ls/add/show/rm/status`,
  `vmlab up`, `vmlab down`, `vmlab with`, `vmlab orphans [--destroy]`.
- **Parallels provider** (`internal/provider/parallels`) — drives
  `prlctl` locally or over SSH; tools-readiness poll lives in the
  provider; transport-side `parallels-guest` solves the layered
  ssh → remote shell → `prlctl exec` quoting once via `posixQuote`.
  Live-smoked end-to-end against a remote Win11 guest.
- **Hetzner provider** (`internal/provider/hetzner`) — shells out to the
  `hcloud` CLI; default `dispose=destroy` for "no surprise spend"; servers
  tagged `vmlab=<name>` on create. `vmlab orphans` sweeps these tags.
  **MVP — code + tests cover the path, live-token validation pending.**
- **`ssh` transport** (`internal/transport/ssh.go`) — plain OpenSSH client,
  the canonical transport for cloud Linux instances emitted by Hetzner Up.
- MCP write-mode tools: `vmlab_up`, `vmlab_down`, `vmlab_with`, plus
  read-only `vmlab_instances`. Gated by `--allow-write`.
- Docs: `docs/providers.md`, `docs/runbooks/parallels.md`,
  `docs/runbooks/hetzner.md`, transport pages for `parallels-guest`
  and `ssh`.
- Evidence: `vmlab with` and `vmlab run @<instance>` now write a run
  directory with a `lifecycle` block in `meta.json` (instance, provider,
  priorState, changed, up/run/down milliseconds), plus
  `status-before.txt` / `status-after.txt` snapshots.
- `vmlab run @<instance>` short-circuit: when the selector is a single
  `@name` matching an instance, lifecycle-wrap the run (Up → flow/cmd →
  Down per disposition). Falls back to the existing `@tag` behaviour
  for non-instance names.
- Flow `exec:` step — argv list passed directly to the transport, no
  `sh -lc` wrapping. Unblocks Windows / non-POSIX guests
  (`exec: ["cmd.exe", "/c", "ver"]`).
- Instance file lock (`internal/state`) serialises `vmlab up/down/with`
  and `vmlab run @<instance>` so two terminals can't race lifecycle
  on the same VM. Contention prints a one-line wait notice naming
  the holder PID, then blocks until the lock is released.
- Top-level Makefile with `build`, `install`, `test`, `vet`, `cover`,
  `smoke-parallels`, `clean` targets.

### Added (post-initial)

- JSON-schema validation for target and flow YAML (santhosh-tekuri/jsonschema),
  with line/path errors on bad files.
- `--dry-run` on `vmlab run` — prints the resolved plan (targets + steps).
- `--verbose` / `-v` root flag — enables DEBUG `log/slog` output.
- `junit.xml` written into every evidence bundle; per-step failure
  attribution when running flows.
- Adapter tests with PATH-injected fake binaries for crabbox, adb, idb,
  simctl, maestro, abx, guiport.
- MCP server now uses `github.com/mark3labs/mcp-go` (proper initialise
  handshake, typed tool descriptors, sampling-ready).

### Added

- Cobra-based CLI: `init`, `target`, `doctor`, `run`, `shell`, `web`, `gui`,
  `screenshot`, `evidence`, `serve`, `version`.
- Transport interface with adapters for `crabbox`, `abx`, `guiport`, `adb`,
  `idb`, `simctl`, `maestro`, plus a `local` transport for self-targeting.
- YAML target registry with user (`~/.vmlab/targets/`) + repo (`.vmlab/targets/`)
  layering.
- Tag selector grammar (`all`, `<name>`, `@tag`, `@a,@b`, `not:@tag`, `a;b`).
- Minimal flow YAML (`run` / `assert` steps) executed via the chosen transport.
- Fan-out runner with prefixed streamed output, `--max-parallel`, `--fail-fast`,
  `--continue-on-error`.
- Evidence bundles under `~/.vmlab/runs/<run-id>/` with per-target stdout,
  stderr, step JSON, and `meta.json`. `evidence ls/show/bundle/prune`.
- MCP server (`vmlab serve --mcp`) over stdio with read-only tools by default
  and write-mode tools (`vmlab_run`, `vmlab_web`, `vmlab_gui`) gated by
  `--allow-write`.
- GoReleaser config + Homebrew tap formula.
