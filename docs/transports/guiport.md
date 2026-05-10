# transport: guiport

Native macOS desktop UI driving via [`guiport`](https://github.com/edihasaj/guiport).

## Settings

```yaml
name: calculator
transport: guiport
tags: [mac, desktop]
guiport:
  app: Calculator
  strict: false   # set true to disable visual fallback
```

## Capabilities

| Capability | Supported |
|---|---|
| gui | yes |
| screenshot | yes |
| shell | no |

## Actions

```sh
vmlab gui calculator --kind click --selector 'AXButton[title="9"]'
vmlab gui calculator --kind type --text "vmlab"
vmlab gui calculator --kind screenshot --path /tmp/calc.png
vmlab gui calculator --kind run --path flows/calc-smoke.yaml
```

Selectors follow guiport's `role[attr=value]` syntax. Prefer `identifier=`
selectors over `name=` — they survive layout/locale changes.
