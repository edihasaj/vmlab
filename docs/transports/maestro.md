# transport: maestro

Declarative mobile flows via [Maestro](https://maestro.mobile.dev).

## Settings

```yaml
name: pixel-maestro
transport: maestro
tags: [android, mobile]
maestro:
  device: emulator-5554   # optional
  host: 127.0.0.1         # optional
  port: 7001              # optional
```

## Capabilities

| Capability | Supported |
|---|---|
| mobile | yes |
| screenshot | yes |
| gui | yes (kind=run) |

## Usage

```sh
# Run a Maestro flow YAML — first arg ending in .yaml/.yml routes to `maestro test`
vmlab run pixel-maestro flows/login.yaml

# Or invoke any maestro subcommand
vmlab run pixel-maestro -- hierarchy

# As a GUI run-flow action
vmlab gui pixel-maestro --kind run --path flows/login.yaml
```
