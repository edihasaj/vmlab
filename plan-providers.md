# vmlab — providers & lifecycle plan

Plan for adding a Provider abstraction (lifecycle: Status / Up / Down /
Destroy) on top of the existing Transport layer (exec).

Status: proposed. Smoke validated end-to-end against the Studio Parallels
Win11 guest via `scripts/smoke-parallels.sh` — see
`~/.vmlab/runs/20260512T073940-parallels-smoke/`.

## North star

Two cleanly separated concepts:

- **Provider** — owns *lifecycle*. Idempotent. Emits a Target.
- **Transport** (existing) — owns *exec*. Unchanged.

They meet at `Target`. Every Provider call starts with a cheap `Status()`
and no-ops if already in the desired state. "Fast if already on" is a
property of the interface, not of every caller.

## Interface sketch

```go
// internal/provider/provider.go
type Provider interface {
    Name() string
    Doctor(ctx context.Context) Health
    Status(ctx context.Context, i Instance) (State, error)
    Up(ctx context.Context, i Instance) (Target, EnsureResult, error)  // idempotent
    Down(ctx context.Context, i Instance, d Dispose) error             // idempotent
}

type State int
const (
    StateUnknown State = iota
    StateNotFound
    StateStopped
    StateSuspended
    StateStarting
    StateRunning
    StateReady
)

type Dispose int
const (
    DisposeKeep Dispose = iota
    DisposeSuspend
    DisposePowerOff
    DisposeDestroy
)

type EnsureResult struct {
    Changed    bool   // did we actually power-on? drives cleanup
    PriorState State
    Reason     string
}
```

`EnsureResult.Changed` is the lesson from the smoke (`resumed_by_us`).
Cleanup respects it so we never suspend a VM the user was already using.

## Instance config

```yaml
# ~/.vmlab/instances/win11-studio.yaml
name: win11-studio
provider: parallels
parallels:
  host: edis-mac-studio     # ssh host, or empty for local
  vm: "Windows 11"
ready:
  kind: parallels-tools     # | ssh | tcp:22 | http
  timeout: 120s
target:
  transport: parallels-guest # new transport — shell-quoted prlctl exec wrapper
disposition:
  on_success: suspend       # keep | suspend | poweroff | destroy
  on_failure: suspend
  only_if_we_started: true
```

## New CLI surface

```
vmlab provider ls / doctor
vmlab instance add / ls / status <name>
vmlab up   <name>                  # idempotent, fast if already Ready
vmlab down <name> [--dispose=...]  # idempotent
vmlab with <name> -- <cmd>         # auto up -> run -> restore prior state
vmlab run @win11-studio flow.yaml  # flow-level bookends per disposition
```

`vmlab with` is the killer ergonomic — agents say "run X on win11-studio"
and lifecycle bookends itself.

## Phases — each ships green on its own

### Phase 1 — Foundation (~0.5d)

No providers yet, just shape.

- `internal/provider/provider.go` — interface + `State` / `Dispose` /
  `EnsureResult` types
- `internal/instance/` — loader + JSON-schema validation (mirrors
  `internal/target/` pattern)
- New commands: `vmlab provider ls`, `vmlab instance ls / status / add`
- Tests: schema, instance round-trip

**Exit:** can declare instances, list them, schema-validate them.
No real provider behavior yet.

### Phase 2 — Parallels provider + `parallels-guest` transport (~1d)

Port the bash smoke to Go.

- `internal/provider/parallels/` — `prlctl` over SSH; Status / Up / Down
  + tools-ready poll
- `internal/transport/parallels_guest.go` — `prlctl exec` wrapper with
  **proper shell-quoting** (the layered-quoting issue from the bash
  smoke — Go solves this cleanly with `[]string` + a `shellquote.Join`
  helper)
- New commands: `vmlab up`, `vmlab down`, `vmlab with`
- Stub-`prlctl`-on-PATH unit tests (matches existing adapter test pattern
  in `internal/transport/stub_test.go`)
- `make smoke-parallels` — real-target test against Studio Win11
- Retire or keep `scripts/smoke-parallels.sh` as a portable sanity check

**Exit:** `vmlab with win11-studio -- "ver"` fully drives the Studio
Win11 lifecycle from the laptop. Idempotent. Always restores prior state.

### Phase 3 — Hetzner provider + `ssh` transport (~1d)

Validates the interface against a fundamentally different lifecycle
(create-from-scratch + cloud-init + delete-when-done).

- `internal/transport/ssh.go` — **vmlab has no plain SSH transport
  today**; required for any cloud Linux box. Plain key-auth, known-hosts
  pinning, evidence-friendly streaming output.
- `internal/provider/hetzner/` — `hcloud` CLI shell-out (no Go SDK lock-in)
- Cloud-init bootstrap: inject pubkey, wait for `:22`, verify
- `dispose: destroy` default — the "no surprise charges" guarantee
- Records uptime window in evidence `meta.json` (foundation for cost
  reporting)

**Exit:** `vmlab with hetzner-smoke -- "uname -a"` provisions a real
Hetzner box, runs the command, destroys the box. Zero residual spend.

### Phase 4 — Cross-cutting polish (~0.5d)

- `vmlab orphans` — list resources tagged `vmlab=<run-id>` that survived
  a crash (cost safety net)
- MCP write-mode tools: `vmlab_up`, `vmlab_down`, `vmlab_with` (gated by
  `--allow-write`, matching existing pattern)
- Docs: `docs/providers.md`, `docs/runbooks/parallels.md`,
  `docs/runbooks/hetzner.md`

**Exit:** v0.1.0 — providers GA for Parallels + Hetzner.

## Extensibility hooks (design in now, build later)

- **Snapshots** — optional `Snapshot(name)` / `Restore(name)` capability.
  Parallels + Hetzner + EC2 all support it.
- **Image baking** — `vmlab image build` runs a flow, emits a
  snapshot/AMI. Future Up calls become seconds.
- **Multi-instance groups** — `@instance-group` auto-spawns N fresh
  boxes, fans flow out, destroys all. Existing fleet layer handles
  fan-out; Provider just needs concurrent-Up safety.
- **Cost guardrails** — `~/.vmlab/budget.yaml` daily cap; vmlab refuses
  Up if projected spend exceeds.
- **Bootstrap hooks** — `pre-up` / `post-up` / `pre-down` for custom
  agent install, secret injection via 1Password.
- **More providers** — `tart` (Apple Silicon), `multipass` / `lima`
  (local Linux), `aws-ec2`, `gcp-gce`. All conform to same interface;
  cloud-init is the common bootstrap path.

## Tradeoffs picked

1. **`hcloud` CLI over Go SDK** — matches vmlab's "compose CLIs"
   philosophy, no SDK churn, slower per-call but Up/Down are infrequent.
2. **YAML instance files + small `~/.vmlab/state/<instance>.json`
   cache** — defer SQLite until concurrency pain is real.
3. **Provider records `EnsureResult.Changed`; disposition logic
   respects it** — only safe way to never surprise-suspend an
   in-use VM.
4. **`parallels-guest` runs as `nt authority\system`** — fine for
   testing, documented loudly. Real non-SYSTEM use moves to the `ssh`
   transport once Win11 OpenSSH is enabled inside the guest.

## Order of execution

P1 → P2 → P3 → P4. Each phase mergeable on its own, CI green, smoke
green. P1+P2 is the "real value today" milestone. P3 unlocks the
cloud-cost case. P4 polishes for v0.1.0.

Realistic estimate: ~3 working days if no surprises.

## Lessons banked from the bash smoke

These shape design choices in P2:

1. **Quoting across ssh -> remote shell -> prlctl exec is layered hell.**
   The Go transport accepts `[]string` and does exactly one round of
   shell-quoting. Bash can't model this cleanly; Go can.
2. **Tools-readiness polling is a real lifecycle phase**, not an
   implementation detail. `ready:` block in the instance config; the
   poll loop lives in the provider, not the caller.
3. **Cleanup must be trap-based.** Anything that resumed a VM must
   suspend it on the way out, even on panic. Mirrors the `trap cleanup
   EXIT` in the bash smoke; in Go, a deferred restore inside `vmlab with`.
4. **Evidence convention from the smoke maps 1:1 onto existing
   `~/.vmlab/runs/<run-id>/`.** Re-use it — meta.json + per-step files
   + `status-before.txt` / `status-after.txt`.
