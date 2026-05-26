# Changelog

All notable changes to vmlab will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added (guiport transport — new action kinds)

Pass through the new `guiport` subcommands (≥0.2) so flows can drive
headless / FileProvider-backed macOS apps without dropping back to
`shell:` steps. Targets that bind to a `guiport` transport now accept
these action `kind` values:

- `launch` / `quit` / `kill` / `restart` — wraps
  `guiport lifecycle <verb> --app <id>`. `--app` is sourced from the
  target's `guiport.app` setting. Replaces previous flows that mixed
  `shell: open /Applications/Foo.app` with `shell: osascript -e 'tell
  application "Foo" to quit'`.
- `logs` — wraps `guiport logs`. All filter flags
  (`process`, `subsystem`, `category`, `last`, `limit`) are passed
  via `extra:` and auto-piped as `--<key> <value>`.
- `fs-create` / `fs-rename` / `fs-trash` / `fs-reveal` — wraps
  `guiport fs <verb>`. Writes flow through Finder/AppleScript so
  FileProvider hooks (`createItem`, `modifyItem`, `deleteItem`) fire
  the same as a real user action. Raw `rm` / `mv` in `shell:` steps
  bypassed those hooks and produced orphaned mappings; this closes
  that gap for cloud-sync E2E flows. Flags (`src`, `into`, `path`,
  `to`) go via `extra:`.

### Added (cloud pricing — `Priced` for every cloud)

Every cloud provider now implements `provider.Priced`, so the
`budget.hourlyUSD` cap actually fires against real quoted rates
instead of acting as documentation only.

- **AWS** — `aws pricing get-products` (us-east-1 endpoint). Filters
  by `instanceType` + `regionCode`. OS / tenancy honoured via
  `ec2.os` / `ec2.tenancy`. Memoised per process.
- **Azure** — public Retail Prices API at `prices.azure.com` (no
  auth). Filters by `armSkuName` + `armRegionName`. Picks the
  lowest non-Spot consumption tier.
- **Hetzner** — `hcloud server-type list -o json` already publishes
  `price_hourly.gross` per (type, location). EUR→USD conversion
  via `HETZNER_EUR_USD` env (default 1.07).
- **GCP** — `gcp.hourlyUSD` instance override only. The Cloud
  Billing Catalog API integration is left as a follow-up since SKU
  matching against fuzzy product names is error-prone; the override
  is the honest path until a live integration lands.

`budget.hourlyUSD` now does what it claimed all along: a real cap
that refuses Up when a misconfigured instance type or region would
quietly bill above the operator's ceiling. Providers that can't
quote a rate (zero return from `HourlyUSD`) still fall through
cleanly — no fail-closed surprises.

### Added (cloud snapshots — AWS, Azure, GCP)

Three providers now implement `provider.Snapshotter`, lifting the
capability matrix from "Parallels + Hetzner only" to "every supported
cloud + Parallels". `vmlab snapshot save/ls/restore/rm` works across
all five today.

- **AWS** — `aws ec2 create-image` produces an AMI tagged
  `vmlab-image=<name>` / `vmlab-source=<instance>`. Cleanup
  deregisters the AMI and deletes the backing EBS snapshots. Set
  `ec2.snapshotNoReboot: true` for a hot snapshot at the cost of
  filesystem consistency. Restore returns a clear hint to set
  `ec2.imageId` on a fresh instance YAML (AWS doesn't restore in-place).
- **Azure** — defaults to OS-disk snapshot (`az snapshot create`),
  non-disruptive on a running VM. Opt into the full managed-image
  path with `azure.snapshotMode: image`; vmlab deallocates first.
  List/Delete combine both shapes. Restore hint: `azure.osDiskId`.
- **GCP** — `gcloud compute machine-images create` captures disks +
  metadata in one resource; cheaper to restore from than per-disk
  snapshots. List/Delete filter by `labels.vmlab-image`. Restore
  hint: `gcp.sourceMachineImage`.

Tests use the existing stub-binary pattern (no live cloud calls):
each provider's snapshot/list/delete/restore paths get unit coverage
against canned CLI output. `docs/providers.md` matrix updated.

### Added (live log streaming over MCP)

- **`vmlab_run_status` MCP tool** (read-only) — returns a run's running
  flag, partial target stats, and exit code when finished. Cheap enough
  to poll once a second from an agent while a long flow is in flight.
- **`vmlab_log_stream` MCP tool** (read-only) — cursor-based tail of a
  run's per-target stdout/stderr logs. Long-polls server-side up to
  `waitSeconds` for new bytes (250ms ticks; no fsnotify dep). Cursor
  is an opaque string the agent passes back to resume; vmlab handles
  the per-target byte-offset bookkeeping internally.
- **`vmlab_run background: true`** — detaches the run via `setsid` so it
  survives the MCP server going away mid-flow. Pre-allocates the run
  id via `VMLAB_RUN_ID`, writes a seed `running.lock`, returns the id
  immediately. Agent then polls with the two read tools above.
- Evidence: new `RunStatus`, `ReadLogChunks`, `LogCursor` exports for
  any caller that wants polling primitives outside MCP.

### Added (cloud maturity + operational/UX gaps)

- **Cost cap on Up** — every instance accepts a `budget:` block; vmlab's
  `provider.UpEnforced` wrapper refuses Up when the provider's own
  hourly rate (via the new optional `Priced` interface) exceeds
  `budget.hourlyUSD`. Providers that don't know their rate treat the
  cap as documentation only; providers that do (today: none yet, but
  the wiring is in place) fail closed before any state-mutating API.
  Migrated all 8 `pr.Up` callsites in the CLI + MCP to go through
  `UpEnforced`.
- **`vmlab provider validate <provider>`** — dry-runs credentials via
  the provider's cheapest read endpoint. Hetzner implements via
  `hcloud server-type list -o noheader`. Providers that don't
  implement the new `Validator` interface report "not implemented"
  cleanly. Use this before kicking off a long flow against a fresh
  cloud target.
- **Snapshot capability matrix** documented in `docs/providers.md`.
  AWS/Azure/GCP/Tart/Windows providers don't yet implement
  `Snapshotter`; `vmlab snapshot` already returns a clear
  "not supported" error in those cases. Tracks toward AMI/managed-
  image/machine-image support per provider.
- **Per-target doctor timeout** — `vmlab doctor` now uses one
  `context.WithTimeout` per target instead of a shared ctx. One slow
  probe can no longer kill the rest of the fleet's checks.
- **Evidence retention auto-prune** — new config knob
  `evidenceMaxSizeMB`; `vmlab evidence prune --auto` applies both
  `evidenceRetentionDays` (age cutoff) and the size ceiling
  (oldest-first eviction until under cap). `PruneToFitSize` exported
  for callers that want size-only.
- **Per-tool MCP ACLs** — `vmlab serve --mcp --allow-tools name,name,…`
  registers only the listed write tools. `--allow-write` remains as
  the "all writes" shorthand. Read-only tools stay unconditional.
  Lets an agent get exactly `vmlab_run` without `vmlab_orphans_destroy`.
- **`vmlab grant --dry-run`** — prints what it would click / poll
  without opening System Settings. Useful for agent diff/preview
  before actually triggering the TCC flow on the user's screen.

### Added (coverage gaps — Linux Wayland/AT-SPI, remote macOS, Windows elevation)

- **Linux Wayland support** via `ydotool` (mouse + click-at) and `wtype`
  (type + hotkey). Auto-detected at runtime by probing `WAYLAND_DISPLAY`
  / `XDG_SESSION_TYPE` in the remote shell, so a single target works
  across X11 and Wayland sessions. Override with `ssh.backend: x11 |
  wayland`. Wayland's lack of a global window-name search means
  `click` / `click-text` raise a clear error pointing at AT-SPI.
- **Linux AT-SPI integration** via `ssh.uiMode: atspi`. Stages an
  inline Python heredoc that uses `pyatspi3` to walk the desktop
  accessibility tree, match Name / Description against a label, and
  invoke the matching node's primary action. Works under both X11 and
  Wayland because AT-SPI is bus-level. Guest must have `python3-pyatspi`
  (or equivalent) installed.
- **`ssh-mac` transport** — drives a remote macOS host by invoking the
  guest's locally-installed `guiport` over SSH. Same GUI verb table as
  the local guiport transport (click, click-text, type, hotkey,
  observe, tree, screenshot, approve), so flows are portable between
  `mac-local-gui` and remote Mac targets. Screenshots round-trip via
  scp from a guest temp file. Doctor probes the remote `guiport doctor`
  so an agent learns when AX or Screen Recording isn't granted yet.
- **Windows pre-elevation via scheduled task** — new CLI:
  ```sh
  vmlab elevate setup <target>      # one-time, needs admin SSH session
  vmlab elevate status <target>
  ```
  Installs (or refreshes) a SYSTEM-running scheduled task that vmlab can
  trigger from non-elevated SSH sessions thereafter. Set `ssh.elevated:
  true` on the target to route `Run()` calls through the task — vmlab
  stages a PowerShell payload to `C:\ProgramData\vmlab\inbox\next.ps1`,
  fires `schtasks /run`, polls the per-call outbox for the captured
  stdout/stderr/exit-code, and projects them onto the caller's writers.
  UAC's secure desktop is unreachable by design; this trades that
  one-time cost for zero UAC prompts during real flows.
  Configurable via `ssh.elevatedTask` (default `vmlab-elevated`) and
  `ssh.elevatedTimeout` (default 60s).

### Added (agent grants — zero-touch for in-app, single-tap for TCC)

- **`gui:approve` step (cross-OS)** — polls for any consent dialog and
  clicks the first matching button. Defaults cover the buttons most
  consent prompts ship with (`Allow`, `OK`, `Continue`, `Yes`, `Trust`,
  `Open`, …). Override via `extra.allow` / `extra.deny` (deny checked
  first so explicit refuse pre-empts a generic allow). `extra.timeout`
  bounds the poll. Fully automatic — no human in this loop:
  ```yaml
  - gui: { kind: approve, allow: ["Camera", "Notifications"], timeout: 10s }
  ```
  Wired into three transports:
  - **`guiport` (macOS)** — AX click-text per label, full button-name match.
  - **`ssh-windows`** — UIA Name-substring per label, full button-name match.
    UAC's secure desktop remains unreachable by design.
  - **`ssh` (Linux X11)** — xdotool window-name match per label, plus a
    Return-key fallback (the default-button activation gesture). Opt-out
    via `extra.useDefaultKey: false` for strict label matching on guests
    where AT-SPI tooling is installed.
- **`vmlab grant --auto`** — after opening the Privacy & Security pane,
  vmlab activates System Settings and asks guiport to `click-text` the
  binary name so the row is focused/scrolled-to. The human's only step
  is the Touch ID prompt that follows the toggle. Requires guiport to
  already have Accessibility (the bootstrap grant). Best-effort: if
  guiport isn't on PATH or the pane layout doesn't match, the command
  falls back to the existing "open pane and poll" behaviour.
- **MCP `vmlab_grant` tool** — same flags as the CLI (binary, scope,
  auto, noWait, timeout). Returns `needsHumanTouchID: true` while
  polling so an agent UI can render a one-line prompt instead of
  silently waiting.
- **`needsGrant` in `vmlab_gui` errors** — when a guiport / undermouse
  call fails with an "Accessibility not trusted" / "screen recording not
  granted" style message, the MCP error payload includes
  `needsGrant: ["accessibility"]` (etc.), so an agent can chain straight
  into `vmlab_grant` without parsing free-form text.

### Added (hardening — agent-driven UI/test loops)

- Per-step `retries:`, `retry_delay:`, and `timeout:` knobs on every action
  step (`run`/`assert`/`exec`/`install`/`sync`/`gui`). A failure (non-zero
  exit or transport error) re-runs the inner action up to N times with
  `retry_delay` between attempts; `timeout` bounds each individual attempt
  via `context.WithTimeout`. Removes the need to wrap every flaky UI click
  in a shell retry loop. See [`docs/flows.md`](docs/flows.md#step-knobs).
- MCP write-mode handlers (`vmlab_up`, `vmlab_down`, `vmlab_with`) now
  acquire the same per-instance file lock the CLI uses, so an MCP agent
  can't race a local `vmlab with` (or another MCP client) on the same VM.

### Fixed (hardening)

- `runExternal` now surfaces context cancellation distinctly from process
  exit. Previously a ctx-killed `ssh` (or any external tool) reported
  `exit=-1` with no message; doctors and flows now report
  `ssh: context deadline exceeded after Nms` so agents can act on it.
- `ssh` / `ssh-windows` doctor messages include the first line of stderr,
  so `vmlab doctor` surfaces "connect to host X port 22: Operation timed
  out" instead of the bare exit code.
- `adb` doctor message includes adb's stderr when `get-state` fails,
  instead of the static "device offline?" guess.
- Default `vmlab doctor --timeout` raised from 10s to 20s; the previous
  default could race ssh's own `ConnectTimeout=10` and kill the probe
  before it returned a useful error.
- Parallels `waitReady` deadline check now also fires when the outer
  context cancels — previously a slow CI runner could surface
  "context deadline exceeded" instead of "waitReady: timed out after Ns",
  hiding the actual cause. Hardened by `TestWaitReadyTimeout`.

### Added (M5+ — artifact auto-delivery, closes the build→ship loop)

- `artifact:` step gained `output:` (host path per OS where the build
  drops its binary) and `deliver_to:` (path inside the target). When
  both are set, vmlab pushes the picked output file through the target's
  transport (rsync over ssh / ssh-windows) immediately after a
  successful build — including on a cache hit, so a fresh VM still gets
  the artifact. No more hand-rolled `scp` follow-on step. The original
  target's settings are never mutated; delivery uses a per-call clone
  with `ssh.dest` set to `deliver_to`.
- Tests: delivery target carries the right `ssh.dest`, the original
  target stays clean, missing build output produces a clear error, and
  empty `deliver_to` keeps the step pure-build (no syncs).

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
