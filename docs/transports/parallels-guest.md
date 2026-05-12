# transport: parallels-guest

Runs commands inside a Parallels Desktop guest VM via `prlctl exec`. When
`parallels.host` is set, prlctl is invoked over SSH on that Mac.

## Settings

```yaml
name: win11
transport: parallels-guest
tags: [windows]
parallels:
  host: edis-mac-studio                       # optional; empty = local
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
| sync | no |
| install | no |
| screenshot | no |
| gui | no |

Use a separate sync path (rsync via SSH to the host, then prlctl exec) if
you need to push files.

## Quoting

The bash smoke had to layer quotes through ssh → remote shell → prlctl exec.
The Go transport handles this in one place: `posixQuote` wraps each argv
element in single quotes, escaping embedded single quotes via `'\''`. Pass
your command as a `[]string`; do not pre-quote.

## Doctor

`prlctl status "<vm>"` runs on the host. Non-zero exit or missing `ssh`
surfaces as unhealthy.
