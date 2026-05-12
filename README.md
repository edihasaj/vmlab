# vmlab

**One CLI for agents to install, set up, test, and verify software across any reachable target.**

vmlab is a transport-agnostic orchestrator for cross-platform verify loops. It
does not replace [crabbox](https://github.com/edihasaj/crabbox), abx, guiport,
adb, idb, or Maestro — it composes them so a single command works whether the
target is a Hetzner Linux VM, a Parallels Windows guest, a Pixel phone, an iOS
simulator, a Mac mini, or a ChromeOS box.

See [`goal.md`](goal.md) for the why, [`plan.md`](plan.md) for the original
plan, and [`docs/providers.md`](docs/providers.md) for the lifecycle layer
that drives Parallels and Hetzner instances.

## Install

```sh
brew tap edihasaj/tap
brew install vmlab
```

Or build from source:

```sh
go install github.com/edihasaj/vmlab/cmd/vmlab@latest
```

## Quickstart

```sh
# 1. set up dirs and a starter flow
vmlab init

# 2. add a target — local, crabbox, adb, idb, simctl, maestro, abx, guiport
vmlab target add --name dev-mac --transport local --tags local,mac
vmlab target add --name ubuntu-local --transport crabbox --tags linux,vm \
  --set crabbox.configPath=~/.crabbox/ubuntu-local.yaml

# 3. verify health across every transport
vmlab doctor

# 4. run a one-off command
vmlab run dev-mac -- uname -a

# 5. run a flow against everything tagged @linux, in parallel
vmlab run @linux flows/install.yaml --max-parallel 4

# 6. browse the evidence bundle from the last run
vmlab evidence ls
vmlab evidence show <run-id>
```

## Commands

| Command | Purpose |
|---|---|
| `vmlab init` | Create user dirs and a starter `.vmlab.yaml` + `flows/install.yaml`. |
| `vmlab target add/ls/show/rm` | Manage targets (YAML files under `~/.vmlab/targets/`). |
| `vmlab doctor [selector]` | Check transport binaries on PATH and per-target reachability. |
| `vmlab run <selector> <flow.yaml>` | Run a flow against the selected targets. |
| `vmlab run <selector> -- <cmd...>` | Run a shell command across targets. |
| `vmlab shell <target>` | Open an interactive shell on a target. |
| `vmlab web <target> -- <abx-args...>` | Drive a web target via abx. |
| `vmlab gui <target> --kind click --selector ...` | Drive a desktop target via guiport. |
| `vmlab screenshot <target> <out-path>` | Capture a screenshot from any transport that supports it. |
| `vmlab evidence ls/show/bundle/prune` | Inspect or zip per-run evidence directories. |
| `vmlab provider ls/doctor` | List/health-check registered VM providers. |
| `vmlab instance add/ls/show/rm/status` | Manage provider instances under `~/.vmlab/instances/`. |
| `vmlab up <instance>` | Ensure an instance is running and ready (idempotent). |
| `vmlab down <instance> [--dispose=…]` | Dispose of an instance — `keep\|suspend\|poweroff\|destroy`. |
| `vmlab with <instance> -- <cmd>` | Up → run → restore prior state. Honours `disposition.only_if_we_started`. |
| `vmlab sync <instance>` | Wire host-to-guest shares (parallels) / rsync (ssh) per the instance's `mounts:`. |
| `vmlab snapshot save/restore/ls/rm` | Manage instance snapshots (parallels today; Snapshotter is a per-provider opt-in). |
| `vmlab wait <instance>` | Re-poll provider readiness (useful after a guest reboot mid-flow). |
| `vmlab orphans [--destroy]` | List (and optionally clean) cloud resources tagged `vmlab=*`. |
| `vmlab serve --mcp` | Speak Model Context Protocol over stdio for agent integration. |

Every read-style command supports `--json`. Run-style commands return non-zero
on any target failure and write a single evidence bundle (including a
`junit.xml` summary for CI consumption).

Extra `run` flags:
- `--dry-run` prints the resolved plan (targets + steps) without executing.
- `--no-evidence` skips writing the bundle.
- `--max-parallel N`, `--fail-fast`, `--continue-on-error` control fan-out.

Global:
- `-v` / `--verbose` enables DEBUG slog output for transport + flow steps.

## Selectors

```
ubuntu-local              # exact name
@linux                    # tag
@linux,@vm                # AND
not:@ci                   # exclusion (chained with previous selector)
@linux,not:@ci            # AND + exclusion
@linux;@mobile            # union
all                       # everything
```

Multiple top-level args are union: `vmlab doctor a @mobile`.

## Transports

| Transport | Notes |
|---|---|
| `local` | Run on the dev machine itself. Useful for testing flows. |
| `crabbox` | Shells out to `crabbox` for SSH-reachable hosts (Linux/Windows/macOS VMs). |
| `abx` | Headless browser actions. Tagged for `web` capability. |
| `guiport` | Native macOS/iOS desktop UI driving via Accessibility. |
| `adb` | Android devices and AVDs. |
| `idb` | iOS devices via the idb-companion stack. |
| `simctl` | iOS Simulator via `xcrun simctl`. |
| `maestro` | Declarative mobile flows. |

Adding a new transport = ~200 LOC adapter implementing the `Transport`
interface. See [`docs/architecture.md`](docs/architecture.md).

## Flows

Intentionally minimal — push complexity into your shell scripts.

```yaml
# flows/install.yaml
name: install
steps:
  - run: ./scripts/setup.sh
  - run: ./scripts/install.sh
  - assert: ./scripts/verify.sh
```

## MCP for agents

```sh
vmlab serve --mcp                # read-only tools
vmlab serve --mcp --allow-write  # adds vmlab_run, vmlab_web, vmlab_gui
```

Exposed tools: `vmlab_targets`, `vmlab_doctor`, `vmlab_evidence`,
`vmlab_run`, `vmlab_web`, `vmlab_gui`. Each returns JSON inside an MCP `text`
content block.

Wire up Claude Code:

```jsonc
// ~/.claude/settings.json
{
  "mcpServers": {
    "vmlab": {
      "command": "vmlab",
      "args": ["serve", "--mcp", "--allow-write"]
    }
  }
}
```

## Configuration

- `~/.vmlab/config.yaml` — user-level defaults.
- `<repo>/.vmlab.yaml` — repo overrides.
- `~/.vmlab/targets/*.yaml` — user targets; repo `.vmlab/targets/*.yaml` shadow them.
- `~/.vmlab/runs/<run-id>/` — evidence bundles (kept 30 days by default).

## License

MIT. See [LICENSE](LICENSE).

Author: [Edi Hasaj](https://edihasaj.com).
