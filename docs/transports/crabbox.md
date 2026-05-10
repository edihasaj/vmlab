# transport: crabbox

Drives any SSH-reachable host through the [`crabbox`](https://github.com/edihasaj/crabbox) CLI.

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

## Notes

- vmlab never owns crabbox credentials — it forwards to the crabbox CLI.
- Use `vmlab shell <name>` for interactive sessions; it execs into `crabbox ssh`.
