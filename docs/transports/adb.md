# transport: adb

Android devices and AVDs via `adb`.

## Settings

```yaml
name: pixel-7
transport: adb
tags: [android, mobile]
adb:
  serial: "RFNX..."   # optional; pin a specific device
```

## Capabilities

| Capability | Supported |
|---|---|
| shell | yes |
| install | yes |
| screenshot | yes (PNG via screencap) |
| mobile | yes |
| gui | no (use Maestro) |

## Routing

`vmlab run <android-target> ...` routes by first arg:

| First arg | Forwarded as |
|---|---|
| `shell` `install` `uninstall` `push` `pull` `logcat` `reboot` `forward` `reverse` | passed verbatim |
| anything else | `adb shell <args joined>` |

So both `vmlab run pixel-7 -- shell whoami` and `vmlab run pixel-7 -- whoami`
work; the second routes through `adb shell`.

## Doctor

Runs `adb get-state` against the configured serial — fails fast if the device
is offline.
