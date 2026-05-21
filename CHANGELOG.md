# Changelog

All notable changes to vmlab will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added (M6 — agent ergonomics + north-star command)

- `vmlab matrix run <selector> <flow-or-cmd>` — the north-star CLI surface.
  One-shot or loop-mode. Sugar over the existing run/watch internals so
  the lifecycle / lock / evidence / notifier code stays single-sourced.
  Supported flags: `--watch`, `--src` (repeatable), `--interval`,
  `--once`, `--from-snapshot`, `--retries`, and `--pass` for arbitrary
  forwarded flags. The goal-statement invocation
  `vmlab matrix run @@app-test ./flow.yaml --watch --from-snapshot=clean`
  now works as documented.
- MCP tool `vmlab_matrix_run` — accepts any vmlab selector (`@<inst>`,
  `@@<tag>`, target name, or `all`) and returns the compact ND-JSON
  matrix output (one row per target/instance, 40-line stderr tail on
  failure). Single low-token agent entry-point for the whole fix-loop.
- `vmlab evidence diff <run-a> <run-b>` — shows only targets whose
  pass/fail status changed (`fixed`, `regressed`, `still-failing`,
  `new`, `gone`). Failing rows include the stderr tail. `--json` flag
  for machine consumption.
- Discord matrix-table posts. When the run path emits `--format=matrix`
  the notifier suppresses the per-phase Start/Success/Failure messages
  and posts ONE aggregate code-block table at the end: status / exit /
  duration per target, ✅ when every row passes, ❌ + configured
  mention when any row fails. Implemented via a new optional
  `Event.Matrix` payload so other notifiers (future Slack, Telegram)
  silently fall back to a normal Phase event without code changes.
- Stretch still deferred: a persistent `matrix_watch` MCP tool (long-
  lived streams need a different MCP protocol shape than synchronous
  tool calls — agents should loop `matrix_run` instead today).

### Added (M5 — artifact build primitive)

- Flow step `artifact:` — declares a host-side build that produces the
  binary the target is about to install/run. Per-OS `build:` map picks
  the right command (e.g. `GOOS=linux go build …` on a Mac host).
- Cache keyed by sha256(src content + build cmd + os + arch); cache
  entries live under `~/.vmlab/cache/artifacts/<key>.json` (overridable
  via `VMLAB_ARTIFACT_CACHE`). A one-line source change rebuilds only
  the touched-OS artifact; everything else cache-hits in a stat-walk.
- Cross-compile escape hatch: build commands always run on the host, so
  Mac → Linux/Windows binaries don't need the target VM to be up.

### Added (M4 — cross-OS flows)

- Flow steps gained `when:` and `install:`. `when:` is an AND-joined list
  of `key=value` / `key!=value` clauses (keys: `os`, `arch`); non-matching
  steps are recorded as `skip` rows rather than failures. `install:` is a
  per-OS map (`{mac/darwin, linux, windows, ios, android}`) that
  auto-dispatches to the right package manager — Windows entries are
  run through PowerShell, everything else through `sh -lc`.
- `target.OSKind()` resolves the guest OS for `when:` / `install:`. Honors
  an explicit `os:` setting on the target/instance, otherwise derives from
  the transport (ssh → linux, ssh-windows → windows, simctl → ios, etc).
- Flow schema accepts `when` and `install` and treats either as a valid
  "this step does something" trigger; existing run/assert/exec semantics
  are unchanged.
- `examples/flows/cross-os-app.yaml` — single flow that detects + smokes
  on Mac, Linux, and Windows targets.

### Added (M2 — iteration loop)

- `vmlab watch <selector> <flow-or-cmd> [--src dir]...` — polls --src
  paths every --interval (default 1s) and re-invokes `vmlab run … 
  --format=matrix` whenever the file-tree hash flips. Skips hidden
  directories (`.git`, `.vmlab`, etc.) so editor lockfiles don't churn
  the loop. Defaults --src to `.` and auto-includes the flow YAML when
  the second arg is a path. Unchanged ticks are silent and finish in
  <1ms (just a stat-walk + hash compare) — agents pay essentially zero
  tokens while idle.
- Subprocess invokes the running vmlab binary so the watch loop reuses
  `run`'s full lifecycle (lock, evidence, notifier, hooks) without
  duplicating logic. `--pass` forwards extra flags (e.g.
  `--pass=--from-snapshot=clean`).
- `--once` flag for single-shot script / CI integration.

### Added (M2 — matrix output, first slice)

- `vmlab run --format=matrix` — newline-delimited JSON output with one
  row per target (or per instance in `@<inst>` / `@@<tag>` mode). Each
  row carries target/transport/provider, status (`pass`/`fail`),
  exit_code, duration_ms; failing rows additionally include the
  reported error and a 40-line stderr tail pulled from the evidence
  bundle. Wired into all three run paths (target selector, single
  instance, fleet). Coexists with the existing `--json` aggregate.
- Caps stderr tail at 40 lines so agents pay near-zero token cost per
  iteration; full logs remain on disk under `runs/<id>/targets/<t>/`.

### Added (M3 — clean-state guarantees)

- `--from-snapshot=<name>` flag on `vmlab with`, `vmlab run @<inst>`, and
  `vmlab run @@<tag>`. Restores the named provider snapshot before Up so
  each iteration starts from an identical clean baseline. Errors clearly
  if the provider does not implement `provider.Snapshotter`. No-op when
  the flag is empty so existing call-sites keep working.
- `vmlab image build @@<tag> <flow.yaml>` — matrix bake. Resolves
  `@@<tag>` to every matching instance and bakes the same `--name` image
  into each provider's snapshot namespace (serial — parallel image bakes
  are a footgun on shared hypervisors). Prints a per-instance summary;
  on first failure remaining instances are reported as SKIPPED.

### Added (M1 — Windows parity, first slice)

- `transport/ssh-windows` — runs commands on Windows hosts over OpenSSH
  server. Default path encodes the PowerShell pipeline as `-EncodedCommand`
  (UTF-16LE base64) so neither cmd.exe nor PowerShell re-parses anything;
  closes the layered-quoting hole that broke arbitrary args on remote
  Windows. Settings: `ssh.shell` (`pwsh`|`powershell`|`cmd`|`none`),
  plus the usual `ssh.host/user/port/identity/knownHosts/strictHost`.
  Supports Run, Doctor, Shell, Sync (rsync / scp).
- `provider/windows` — thin façade for already-provisioned Windows hosts
  (bare-metal labs, externally-managed Hyper-V VMs). Never owns
  power-state; `Status` maps to `ssh-windows` reachability, `Up` asserts
  reachability and emits an `ssh-windows` target with `Administrator` as
  the default SSH user, `Down` only honours `DisposeKeep`. For cloud
  Windows keep using `aws`/`azure`/`gcp` and set `target.transport:
  ssh-windows` on the instance.
- Schemas: `ssh-windows` added to the transport enum (targets +
  instances); `windows` added to the provider enum.
- `examples/targets/windows-host.yaml` — starter target for a remote
  Windows lab box.

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
