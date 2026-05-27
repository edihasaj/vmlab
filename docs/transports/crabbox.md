# transport: crabbox

Drives any crabbox-managed lease through the [`crabbox`](https://github.com/openclaw/crabbox) CLI.

## Settings

```yaml
name: ubuntu-local
transport: crabbox
tags: [linux, local, vm]
crabbox:
  configPath: ~/.crabbox/ubuntu-local.yaml   # OR
  name: ubuntu-local                          # OR
  host: 10.0.0.5                              # inline static
  user: ubuntu
  port: 22
```

Exactly one of `configPath`, `name`, or the `host`/`user`/`port` triple should
be set. Other crabbox-specific tuning lives in the referenced crabbox config.

## Capabilities

| Capability | Supported |
|---|---|
| shell | yes |
| sync | yes |
| install | yes |
| screenshot | no |
| gui | no |

## Doctor

`crabbox doctor` is invoked with the resolved config/name. Non-zero exit
surfaces as an unhealthy target.

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
