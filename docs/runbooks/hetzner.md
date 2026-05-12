# Runbook: Hetzner provider

End-to-end: provision a Hetzner Cloud server from scratch, run a command on
it, destroy it. Zero residual spend.

## Prerequisites

- `hcloud` CLI on PATH (`brew install hcloud`).
- Hetzner API token: `hcloud context create vmlab` then `hcloud context use
  vmlab`, **or** export `HCLOUD_TOKEN`, **or** set `hetzner.token` on the
  instance.
- An SSH key registered in your Hetzner project (name, not local path) —
  vmlab passes this with `--ssh-key`.

## Configure the instance

```bash
vmlab instance add \
  --name smoke-arm \
  --provider hetzner \
  --tags linux,cloud \
  --set hetzner.serverType=cax11 \
  --set hetzner.image=debian-12 \
  --set hetzner.location=fsn1 \
  --set hetzner.sshKey=edi \
  --set ssh.identity=~/.ssh/id_ed25519 \
  --set target.transport=ssh \
  --set ready.kind=tcp:22 \
  --set ready.timeout=180s \
  --set disposition.on_success=destroy \
  --set disposition.on_failure=destroy
```

`disposition.on_success=destroy` is the default for "no surprise spend".

## Smoke

```bash
vmlab instance status smoke-arm
vmlab with smoke-arm -- uname -a
# server is gone after the with completes
vmlab orphans                  # should be empty
```

## How it works

- `Status` runs `hcloud server describe <name> -o json` and maps `.status`
  to `provider.State`; "Server not found" → `StateNotFound`.
- `Up`:
  1. If absent → `hcloud server create --name --type --image [--location]
     [--ssh-key] [--user-data-from-file] --label vmlab=<name>`.
  2. If stopped → `hcloud server poweron`.
  3. Polls TCP `:22` until reachable (`net.Dial`) or `ready.timeout`.
  4. Emits a `Target` with transport `ssh` pointing at the box's public IP.
- `Down` rejects `suspend` (Hetzner has no suspend); `poweroff` powers it
  off, `destroy` runs `hcloud server delete`.

## Cost safety

- Every server is tagged `vmlab=<name>` on create.
- `vmlab orphans` lists tagged servers across configured providers; pass
  `--destroy` for a sweep.
- `with --dispose=destroy` is the default disposition path.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `hcloud: command not found` | `brew install hcloud` |
| `unauthorized` | no `HCLOUD_TOKEN`, no `hcloud context`, no `hetzner.token` |
| `ssh: connect to host …: Permission denied (publickey)` | `hetzner.sshKey` not matching a key that holds `ssh.identity`'s public half |
| Server lingers after crash | `vmlab orphans --destroy` cleans up |
