# transport: simctl

iOS Simulator via `xcrun simctl`.

## Settings

```yaml
name: ios-sim
transport: simctl
tags: [ios, simulator, mobile]
simctl:
  udid: "00000000-0000-0000-0000-000000000000"   # or omit to use "booted"
```

## Capabilities

| Capability | Supported |
|---|---|
| install | yes |
| screenshot | yes |
| mobile | yes |

## Common verbs

`vmlab run ios-sim -- <verb>` translates the first argument:

| Verb | Mapped to |
|---|---|
| `boot` `shutdown` `install` `uninstall` `launch` `terminate` `openurl` | `xcrun simctl <verb> <udid|booted> <rest>` |
| anything else | `xcrun simctl <args>` (verbatim) |

Examples:

```sh
vmlab run ios-sim -- boot
vmlab run ios-sim -- install path/to/MyApp.app
vmlab run ios-sim -- launch com.example.app
vmlab screenshot ios-sim /tmp/sim.png
```
