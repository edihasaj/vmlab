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

| Provider | Backend | Default transport | Default `dispose.on_success` | Status |
|---|---|---|---|---|
| `parallels` | `prlctl` (local or over SSH) | `parallels-guest` | `suspend` | live-smoked |
| `hetzner` | `hcloud` CLI | `ssh` | `destroy` | **MVP — code + stub tests; live-token validation pending** |

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
