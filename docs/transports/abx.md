# transport: abx

Web actions via the [`abx`](https://github.com/edihasaj) CLI.

## Settings

```yaml
name: marketing-site
transport: abx
tags: [web]
abx:
  url: https://example.com
  mode: live   # optional; if "live", commands route through `abx live`
```

## Capabilities

| Capability | Supported |
|---|---|
| web | yes |
| screenshot | yes |
| shell | no |

## Usage

```sh
vmlab web marketing-site -- goto https://example.com
vmlab screenshot marketing-site /tmp/site.png
```

For full flow runs, write the abx commands as steps in a flow YAML:

```yaml
steps:
  - run: abx goto https://example.com
  - assert: abx text 'h1' | grep -qi welcome
```
