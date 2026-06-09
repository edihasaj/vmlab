# transport: crabbox

Drives any crabbox-managed lease through the [`crabbox`](https://github.com/openclaw/crabbox) CLI.

## Settings

```yaml
name: ubuntu-local
transport: crabbox
tags: [linux, local, vm]
crabbox:
  id: blue-lobster        # lease id OR friendly slug  -> crabbox -id <v>
  # slug: blue-lobster    # alias for id
  provider: hetzner       # optional: scope to a provider -> -provider <v>
  # --- OR pin a static SSH host (implies provider: ssh) ---
  # staticHost: 10.0.0.5  # aliases: host
  # staticUser: ubuntu    # aliases: user
  # staticPort: "22"      # aliases: port
```

crabbox ≥ 0.21 is lease-based and uses single-dash flags that live *after* the
subcommand. vmlab maps target settings to those flags:

- `id` / `slug` → `-id <v>` (crabbox accepts a lease id or its friendly slug)
- `provider` → `-provider <v>`
- `staticHost`/`staticUser`/`staticPort` (or the legacy `host`/`user`/`port`
  aliases) → `-static-host`/`-static-user`/`-static-port`, defaulting
  `-provider ssh`.

With no `id`/`slug`, vmlab relies on crabbox's repo-local default lease (the
box claimed for the current checkout). There is **no** `configPath`/`--config`
flag — crabbox discovers its config from the working directory.

## Capabilities

| Capability | Supported |
|---|---|
| shell | yes |
| sync | yes |
| install | yes |
| screenshot | yes |
| gui | no |

`screenshot` shells to `crabbox screenshot -id <lease> -output <path>` (desktop
leases). AX/OCR-level GUI driving stays with the `guiport`
transports; a `gui: { kind: screenshot }` step is accepted and routed to the
screenshot path, anything else returns unsupported.

## Doctor

When the target names a concrete address (`id`/`slug` or `staticHost`), vmlab
runs `crabbox status ... -json` to check that destination. Otherwise it falls
back to `crabbox doctor` (global broker/provider readiness). Non-zero exit
surfaces as an unhealthy target.

## Sync

crabbox has no standalone `sync` command — `run` rsyncs the working checkout to
the box on every call. `vmlab sync <crabbox-target>` therefore runs a no-op
remote command (`crabbox run … -- true`) purely to push the current diff.

## Provider passthrough

vmlab exposes selected crabbox subcommands as `vmlab crabbox <sub> [args...]`
so flows and ops can stay inside a single CLI:

| Subcommand | What it does |
|---|---|
| `vmlab crabbox checkpoint create --id <lease> --name <label>` | Provider-native VM snapshot (AWS EBS / AWS AMI / Azure OS disk / GCP disk / Parallels) or workspace tar |
| `vmlab crabbox checkpoint fork <chk_id> [--slug <name>]` | Spin a new lease from a checkpoint — seconds vs. minutes for re-setup |
| `vmlab crabbox checkpoint list` | List local + provider checkpoints |
| `vmlab crabbox warmup --provider parallels --class macos` | Provision/claim a lease, including the new Parallels provider |
| `vmlab crabbox image promote <ami>` | Promote an AWS AMI for future runners (admin-token gated) |
| `vmlab crabbox pool ...` | Manage warm lease pools |

All flags after the subcommand pass through verbatim — see `crabbox <sub>
--help` for the upstream surface. vmlab does not parse, validate, or re-emit
them; it only routes.

## Parallels via crabbox

crabbox ≥ 0.20 ships a managed Parallels provider. Use it when you want
checkpoint-and-fork on local/remote Parallels Desktop hosts (e.g. a spare Mac
Studio acting as the agent fleet):

```sh
vmlab crabbox warmup --provider parallels --class macos
vmlab crabbox run --id <slug> --shell -- 'brew bundle && xcodebuild -resolvePackageDependencies'
vmlab crabbox checkpoint create --id <slug> --name xcode-ready
vmlab crabbox checkpoint fork chk_... --slug update-flow-smoke
```

This is distinct from vmlab's own Parallels provider (`internal/provider/parallels`),
which talks directly to `prlctl` for the VMs vmlab itself manages.
`vmlab snapshot …` operates on those; `vmlab crabbox checkpoint …` operates
on crabbox-brokered leases. Pick by who owns the lease.

## Notes

- vmlab never owns crabbox credentials — it forwards to the crabbox CLI.
- Use `vmlab shell <name>` for interactive sessions; it execs into `crabbox ssh`.
