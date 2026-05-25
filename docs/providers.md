# Providers

Providers own *lifecycle*: bringing an instance to ready, and disposing of it
on the way out. Transports own *exec*. They meet at a `Target` — every
`vmlab up` returns one, and every transport accepts one.

```
Instance (YAML)  ──▶  Provider.Up()  ──▶  Target  ──▶  Transport.Run()
                          │
                          └── Provider.Down() ── disposition
```

## Built-in providers

| Provider | Backend | Default transport | Default `dispose.on_success` | Snapshots | Status |
|---|---|---|---|---|---|
| `parallels` | `prlctl` (local or over SSH) | `parallels-guest` | `suspend` | ✓ native | live-smoked |
| `hetzner` | `hcloud` CLI | `ssh` | `destroy` | ✓ image-based | code + tests; `vmlab provider validate hetzner` dry-runs the token |
| `aws` | `aws` CLI | `ssh` | `destroy` | ✗ (planned: AMI-based) | MVP — `vmlab snapshot` returns "not supported" until added |
| `azure` | `az` CLI | `ssh` | `destroy` | ✗ (planned: managed image) | MVP — same caveat |
| `gcp` | `gcloud` CLI | `ssh` | `destroy` | ✗ (planned: machine image) | MVP — same caveat |
| `tart` | `tart` CLI | `ssh` | `keep` | ✗ (Tart has clone but no in-place snapshot) | MVP |
| `windows` | local Windows / Hyper-V | `ssh-windows` | `keep` | ✗ | MVP |

`vmlab snapshot ls/save/restore/rm` type-asserts the provider for `Snapshotter`
and returns a clear `provider X does not support snapshots` error when the
capability is missing — flows that need snapshots fail loudly instead of
silently succeeding with no checkpoint.

### Cost caps (`budget.hourlyUSD`)

Every instance accepts a `budget:` block; vmlab refuses Up if the provider's
own hourly rate (via the `Priced` interface) exceeds the cap. Providers
that don't know their rate (or don't implement `Priced`) treat the cap as
documentation only — no provider-quoted price, no enforcement, no surprise
block. Set the cap when you mean "guard me against a misconfigured region /
instance type."

```yaml
name: gpu-burst
provider: aws
budget:
  hourlyUSD: 2.50   # refuse Up if AWS quotes > $2.50/hr
```

## Instance config

Lives in `~/.vmlab/instances/<name>.yaml` (and repo overrides in
`.vmlab/instances/<name>.yaml`). Common shape:

```yaml
name: win11-studio
provider: parallels
tags: [windows]

parallels:                  # provider-specific block
  host: edis-mac-studio
  vm: "Windows 11"

ready:
  kind: parallels-tools     # | ssh | tcp:22 | http
  timeout: 120s

target:
  transport: parallels-guest # transport emitted by Up()

disposition:
  on_success: suspend       # keep | suspend | poweroff | destroy
  on_failure: suspend
  only_if_we_started: true  # never suspend a VM the user was already using
```

### Mounts

`mounts:` declares host-to-guest file shares. Each provider wires them up
its own way:

| Provider | Implementation | Guest path |
|---|---|---|
| `parallels` | `prlctl set --shf-host-add` (configured automatically on `vmlab up`) | `\\Mac\<name>` |
| `ssh` / `hetzner` | rsync via `vmlab sync` | `<guest>` (the mount's `guest:` field) |

```yaml
mounts:
  - name: app
    host: /Users/edihasaj/Projects/myapp   # parallels: path on the Mac running Parallels Desktop
    guest: 'Z:\app'                        # informational (Parallels) / rsync target (SSH)
    mode: rw                               # ro | rw  (default rw)
```

**Watch out:** for `parallels` with `host:` set to a remote Mac, the
`mounts[*].host` paths are interpreted on **that remote Mac**, not your
laptop. Create the directory on the Parallels host (or rsync to it
first).

### Snapshots

Providers that implement the optional `Snapshotter` capability expose
`vmlab snapshot save/restore/ls/rm`. Parallels supports it via
`prlctl snapshot*`; Hetzner does not yet (use `hcloud image create`
manually).

`only_if_we_started: true` (default for the `with` flow) is the safety net
from the bash smoke: cleanup is gated by `EnsureResult.Changed`. If the VM
was already running when `Up` ran, `Down` does nothing.

## CLI surface

```
vmlab provider ls                              # registered providers
vmlab provider doctor                          # presence check

vmlab instance ls                              # configured instances
vmlab instance add --name … --provider …
vmlab instance show <name>
vmlab instance status <name>                   # power-state via provider
vmlab instance rm <name>

vmlab up   <name>                              # idempotent
vmlab down <name> [--dispose=…]                # idempotent
vmlab with <name> -- <cmd>                     # up → run → restore

vmlab orphans [--destroy]                      # cost safety net
```

## MCP write-mode

`vmlab serve --allow-write` exposes `vmlab_up`, `vmlab_down`, `vmlab_with`
alongside the existing run/web/gui tools. Read-only `vmlab_instances` is
always available.

## Extensibility

A new provider needs:

1. A package under `internal/provider/<name>/` implementing
   `provider.Provider` (Name / Doctor / Status / Up / Down).
2. A side-effect import in `internal/provider/all/all.go`.
3. The provider name added to `internal/schema/instance.schema.json`'s
   `provider` enum.
4. Adapter tests using PATH-injected fake binaries (mirrors
   `internal/transport/stub_test.go`).

See `internal/provider/parallels/` and `internal/provider/hetzner/` for two
worked examples — one virt-on-laptop, one cloud-and-destroy.
