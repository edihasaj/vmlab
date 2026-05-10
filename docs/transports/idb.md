# transport: idb

iOS devices via [idb](https://github.com/facebook/idb).

## Settings

```yaml
name: my-iphone
transport: idb
tags: [ios, mobile]
idb:
  udid: "00008110-..."
```

## Capabilities

| Capability | Supported |
|---|---|
| install | yes |
| screenshot | yes |
| mobile | yes |
| shell | no |
| gui | no (use Maestro) |

## Doctor

Runs `idb list-targets` (filtered to `--udid` when set) to confirm the device
is paired and the companion is reachable.
