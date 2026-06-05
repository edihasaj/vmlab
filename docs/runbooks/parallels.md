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
  --set parallels.host=mac-studio.local \
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
vmlab restart win11-studio                # reboot + wait for ready
vmlab down win11-studio --dispose=suspend
```

## Driving a remote host's VM from your laptop

Parallels runs on the Mac that owns the VM (often a Mac Studio), not your
laptop. Set `parallels.host` (and `parallels.user`/`parallels.port` if needed)
so vmlab runs `prlctl` over SSH on that host. **Without `parallels.host` vmlab
runs `prlctl` locally** and fails with "Unable to connect to Parallels
Service" on a machine that has no Parallels — that's a config gap, not a bug.

## Pushing files into the guest

Don't hand-encode files through `prlctl exec` — large command lines choke the
ssh → prlctl chain. Use a shared folder: declare a `mounts:` entry (or
`vmlab sync <instance> --path <dir>`) and the host dir appears in the guest at
`\\Mac\<name>`.

## Recovering a wedged guest / Parallels service down

- **Guest agent wedged** (`prlctl exec` returns `PrlResult_GetParamByIndex` or
  hangs mid-run): `vmlab restart <instance>` reboots the guest and waits for
  Parallels Tools — no full down/up needed.
- **Parallels Service not running** on the host: vmlab auto-launches Parallels
  Desktop (`open -ga "Parallels Desktop"`, over SSH when `parallels.host` is
  set) and retries once. Disable with `--set parallels.autostart=false`;
  point at a different app with `--set 'parallels.app=Parallels Desktop'`.

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
| `Unable to connect to Parallels Service` | App not running on the host — vmlab auto-launches + retries; if it persists the host may need a reboot, or set `parallels.host` if you're driving a remote Mac |
| `PrlResult_GetParamByIndex` / exec hangs | Guest agent wedged — `vmlab restart <instance>` |
