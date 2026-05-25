# flows

A flow is a YAML list of shell steps. By design vmlab keeps this YAML
narrow — push conditionals and complex interpolation into your shell
scripts. The handful of cross-cutting knobs that the runner exposes
(retries, per-step timeout, gating) live here so agent-driven loops
don't need to wrap every flaky step.

## Schema

```yaml
name: install        # optional; defaults to filename stem
steps:
  - run: <shell>     # arbitrary shell, runs through `sh -lc`
  - assert: <shell>  # same; semantically marks a verification step
  - exec: [argv...]  # argv list, passed directly — no shell wrapping
  - name: <label>    # optional, attached to any step type
```

Use `exec:` when the target doesn't speak POSIX shell (Windows guests via
`parallels-guest`, anywhere `sh` isn't on PATH). Example:

```yaml
steps:
  - name: ver
    exec: ["cmd.exe", "/c", "ver"]
  - name: date
    exec: ["powershell.exe", "-NoProfile", "-Command", "Get-Date -Format 'o'"]
```

### Step knobs

Every step type accepts these optional fields:

| Field | Type | Purpose |
|---|---|---|
| `name` | string | Human label, recorded in evidence. |
| `when` | string | OS/arch gate (`os=linux`, `arch=arm64,!=x86_64`). Non-match → skipped, not failed. |
| `workdir` | string | cwd inside the guest for `run`/`assert`. Flow-level `workdir:` is the default. |
| `env` | map | `K=V` exported into the step's shell. Merges over flow-level `env:`. |
| `timeout` | duration | Bounds a single attempt (`30s`, `2m`). Empty = inherits the outer run context. |
| `retries` | int | Re-run on failure (non-zero exit OR transport error). `retries: 3` = up to 4 attempts total. |
| `retry_delay` | duration | Wait between attempts. Default `1s` when `retries > 0`. |

```yaml
- name: click-then-poll
  retries: 5
  retry_delay: 250ms
  timeout: 5s
  gui:
    kind: click
    selector: 'button[label="Save"]'
```

### Consent dialogs (gui:approve)

For in-app permission prompts (the target app asking for Camera, Mic,
Notifications, etc — *not* TCC), use the dedicated approve step. It polls
for the dialog and clicks the first matching button. Defaults cover the
common allow-style labels.

```yaml
- gui:
    kind: approve
    extra:
      allow: ["Allow", "Allow While Using App", "Continue"]
      deny:  ["Don't Send", "Not Now"]    # checked first
      timeout: 10s
```

Cross-OS behaviour:

| Transport | Mechanism | Notes |
|---|---|---|
| `guiport` (macOS) | AX click-text per label | Full button-name match. |
| `ssh-windows` | UIA Name-substring per label | Full button-name match. UAC secure-desktop dialogs are out of reach by design. |
| `ssh` (Linux X11) | xdotool window-name match + Return-key fallback | Best-effort. Set `extra.useDefaultKey: false` to disable the Return fallback when you need strict label matching (requires AT-SPI tooling on the guest). |

System TCC prompts on macOS (Touch ID / password) cannot be approved this
way — that's the one human step macOS reserves. Use `vmlab grant --auto`
to navigate to the right toggle so the Touch ID is the only thing the
human does.

## Example

```yaml
name: install-and-smoke
steps:
  - run: ./scripts/setup.sh
  - run: ./scripts/install.sh
  - assert: ./scripts/verify.sh
  - assert: 'systemctl is-active --quiet myservice'
```

## Execution

- Each step runs through the target's transport via `sh -lc <step>`.
- Non-zero exit fails the flow for that target. The `fleet` runner aggregates
  exit codes across targets.
- Per-step output streams to stdout/stderr in real time (with a `[<target>]`
  prefix when run across many targets) and is also captured to the evidence
  bundle under `~/.vmlab/runs/<run-id>/targets/<name>/{stdout,stderr}.log`.
- Step-level results land in `targets/<name>/steps.json`.

## Tips

- Keep `assert` cheap — it runs after `run` and is the natural place for
  smoke checks. The split is purely organizational.
- Reuse the same flow file in CI and locally. vmlab is the inner loop;
  CI is the outer loop.
