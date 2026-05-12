# Runbook: Parallels provider

End-to-end: drive a Parallels Desktop VM (typically on a remote Mac) from
your laptop. The plan ports `scripts/smoke-parallels.sh` to Go.

## Prerequisites

- Parallels Desktop on the host; **Parallels Tools** installed in the guest
  (Windows: from Actions → Install Parallels Tools).
- SSH into the host works in batch mode (`ssh -o BatchMode=yes <host> true`).
- `/Applications/Parallels Desktop.app/Contents/MacOS/prlctl` reachable on
  the host (vmlab prepends this to `$PATH` for you).

## Configure the instance

```bash
vmlab instance add \
  --name win11-studio \
  --provider parallels \
  --tags windows \
  --set parallels.host=edis-mac-studio \
  --set 'parallels.vm=Windows 11' \
  --set target.transport=parallels-guest \
  --set ready.kind=parallels-tools \
  --set ready.timeout=120s \
  --set disposition.on_success=suspend \
  --set disposition.only_if_we_started=true
```

## Smoke

```bash
vmlab instance status win11-studio        # → suspended | running
vmlab up win11-studio                     # idempotent, fast if already on
vmlab with win11-studio -- cmd.exe /c ver
vmlab down win11-studio --dispose=suspend
```

## How it works

- `Status` reads `prlctl status "<vm>"` over SSH; `parseStatus` maps the
  output to `provider.State`.
- `Up` calls `prlctl start "<vm>"` when prior state is suspended/stopped,
  then polls `prlctl exec "<vm>" cmd.exe /c ver` (Parallels Tools readiness)
  until success or `ready.timeout`.
- `Down` honours `Dispose`: `suspend` → `prlctl suspend`, `poweroff` →
  `prlctl stop`, `destroy` → `prlctl stop --kill` then `prlctl delete`.
- All quoting goes through one round of POSIX single-quote escaping
  (`posixQuote`) in `internal/transport/parallels_guest.go` so the
  ssh → remote shell → prlctl exec chain stops eating quotes.

## Cleanup safety

`disposition.only_if_we_started: true` means vmlab will *only* suspend the
VM if it started it. If the user already had the VM running, vmlab leaves
it alone. The signal is `EnsureResult.Changed`, set by `Up`.

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `prlctl: command not found` over SSH | `prlctl.path` setting wrong; default is `/Applications/Parallels Desktop.app/Contents/MacOS` |
| `waitReady: timed out` | Parallels Tools not installed in the guest, or guest is at a logon screen |
| `permission denied (publickey)` | host SSH key-auth not set up; check `ssh -o BatchMode=yes <host>` |
