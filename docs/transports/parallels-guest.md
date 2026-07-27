# transport: parallels-guest

Runs commands inside a Parallels Desktop guest VM via `prlctl exec`. When
`parallels.host` is set, prlctl is invoked over SSH on that Mac.

## Settings

```yaml
name: win11
transport: parallels-guest
tags: [windows]
parallels:
  host: mac-studio.local                       # optional; empty = local
  user: edi                                   # optional ssh user
  port: 22                                    # optional ssh port
  vm: "Windows 11"                            # required
  prlctlPath: /Applications/Parallels Desktop.app/Contents/MacOS
  guiSession: interactive                     # optional; see below
```

Typically you don't create this target directly — it's the default
`target.transport` emitted by the `parallels` provider's `Up`.

## Capabilities

| Capability | Supported |
|---|---|
| shell | no (use `prlctl enter` on the host) |
| sync | yes (adds a Parallels shared folder; remote hosts stage via rsync first) |
| install | no |
| screenshot | yes |
| gui | yes (Windows interactive-session UI Automation) |

`vmlab cp` is available for one-off scripts and configuration files. Its
Windows staging uses deliberately small encoded chunks because Parallels
Desktop 26.4 can silently hang near its `prlctl exec` argument ceiling.
Use `vmlab sync` and the resulting shared folder for larger trees.

## Sessions: why `guiSession: interactive` matters

`prlctl exec` runs in **Session 0** as SYSTEM. Session 0 has no desktop, so
anything started there has `MainWindowHandle = 0` and is invisible to
`screenshot`; `SendKeys` and `mouse_event` likewise no-op against real apps
(Teams, browsers, anything WebView2/Electron).

The failure is quiet, which is what makes it expensive: the command reports
success, and the screenshot shows whatever was already on the desktop.
`AppActivate` and clicking the taskbar won't help — on that desktop the window
does not exist.

Setting `parallels.guiSession: interactive` routes GUI payloads through a
one-shot scheduled task (`schtasks /ru INTERACTIVE /it`) in the logged-in
user's session, where input and windows are real. Set it on any target whose
desktop you intend to drive or screenshot.

## GUI kinds

| Kind | Notes |
|---|---|
| `click` | by UIA AutomationId or Name (`--selector`) |
| `click-text` | substring Name match (`--text`) |
| `click-at` | raw `--x`/`--y`; needed for WebView2 surfaces |
| `type` | SendKeys, falls back to UIA ValuePattern |
| `hotkey` | chord via `--text`, e.g. `ctrl+shift+t` |
| `launch` | start a program on the desktop; `--path` exe + `--text` args, or a full command line in `--text`. Prints `pid=<n>` |
| `open-url` | `Start-Process <url>` (`--path` or `--text`) |
| `observe` / `tree` | read back the focused element / foreground window |
| `screenshot` | needs `--path`; round-trips the PNG to the host |
| `wait` | `--ms` |

`launch` exists because `vmlab run` cannot do it: `run` is Session 0, so a
window it starts is never visible. Use `launch` whenever the point is for a
human (or a screenshot) to see the program.

```sh
vmlab gui win11 --kind launch --path powershell.exe \
  --text "-NoProfile -ExecutionPolicy Bypass -File C:/Users/Public/report.ps1"
```

Note there is no `run` kind here: across transports `run`/`run-flow` means
"execute a guiport flow file", which this transport does not implement.

## Quoting

The bash smoke had to layer quotes through ssh → remote shell → prlctl exec.
The Go transport handles this in one place: `posixQuote` wraps each argv
element in single quotes, escaping embedded single quotes via `'\''`. Pass
your command as a `[]string`; do not pre-quote.

## Doctor

`prlctl status "<vm>"` runs on the host. Non-zero exit or missing `ssh`
surfaces as unhealthy.
