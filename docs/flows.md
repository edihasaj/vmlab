# flows

A flow is a YAML list of shell steps. By design vmlab does not grow this YAML
into a programming language — push retries, conditionals, and env interpolation
into your shell scripts.

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
