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

## Quoting

The bash smoke had to layer quotes through ssh → remote shell → prlctl exec.
The Go transport handles this in one place: `posixQuote` wraps each argv
element in single quotes, escaping embedded single quotes via `'\''`. Pass
your command as a `[]string`; do not pre-quote.

## Doctor

`prlctl status "<vm>"` runs on the host. Non-zero exit or missing `ssh`
surfaces as unhealthy.
