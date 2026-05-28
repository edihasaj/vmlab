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
  # appBundle: /Applications/guiport.app   # override SR app-bundle discovery
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

## Permissions (Accessibility + Screen Recording)

guiport needs two macOS TCC grants:

- **Accessibility** — for AX reads and UI actions (click/type/observe/tree).
  Grant the `guiport` binary directly: `vmlab grant guiport accessibility`.
- **Screen Recording** — for `screenshot` and screenshot-on-failure.

Screen Recording is the awkward one: macOS attributes it to the *responsible*
process, which for a CLI launched from a shell is the **terminal**, not
guiport — so a bare `guiport` can never own the grant (and under a detached
tmux/agent there's no terminal to grant at all). vmlab solves this by routing
capture through a **`guiport.app` bundle**: launched via LaunchServices,
guiport becomes its own responsible process, so the SR grant lands on
`guiport.app` and persists across rebuilds of the same signed bundle.

Setup once:

1. Build a signed `guiport.app` (binary in `Contents/MacOS/`, stable
   `CFBundleIdentifier`, signed with a real identity + hardened runtime) under
   `/Applications` or `~/Applications`. vmlab auto-discovers either, or set
   `guiport.appBundle` on the target.
2. Launch it once (`open -a guiport.app --args screenshot --out /tmp/x.png`) to
   register it with TCC, then toggle **guiport** ON in System Settings →
   Privacy & Security → Screen Recording.

With the bundle present, `vmlab screenshot`/`gui --kind screenshot` route
through it automatically; `vmlab doctor` treats a CLI-level SR miss as healthy
as long as Accessibility is granted and the bundle exists. Without the bundle,
capture falls back to the direct binary (works only if the launching terminal
holds the SR grant).

Build/refresh the bundle with `scripts/guiport-app.sh` (auto-detects your
guiport binary + a codesigning identity). Re-signing with the *same* identity
and bundle id preserves the SR grant, so you only toggle it ON once.

Override discovery with the `guiport.appBundle` target setting, or the
`VMLAB_GUIPORT_APP` env var — a path to a bundle, or `off`/`none`/`0` to force
the direct-binary path (used by the flow tests).
