# vmlab

**One CLI for agents to install, set up, test, and verify software across any reachable target.**

vmlab is a transport-agnostic orchestrator for cross-platform verify loops. It
does not replace [crabbox](https://github.com/edihasaj/crabbox), abx, guiport,
adb, idb, or Maestro — it composes them so a single command works whether the
target is a Hetzner Linux VM, a Parallels Windows guest, a Pixel phone, an iOS
simulator, a Mac mini, or a ChromeOS box.

It provisions the machines too. `vmlab up` **scales an instance up** on
Parallels, Hetzner, AWS, Azure, GCP, or Tart; you run the verify loop on it;
`vmlab down` **scales it back down** (suspend / poweroff / destroy) — with
per-instance budget caps and an `orphans` sweep so nothing is left billing.

See [`docs/architecture.md`](docs/architecture.md) for the design and
[`docs/providers.md`](docs/providers.md) for the lifecycle layer that scales
Parallels and cloud instances up and down.

## Install

Homebrew tap (macOS/Linux):

```sh
brew install edihasaj/tap/vmlab
```

`go install` (Go ≥1.24):

```sh
go install github.com/edihasaj/vmlab/cmd/vmlab@latest
```

Build from source (Go ≥1.24):

```sh
git clone https://github.com/edihasaj/vmlab && cd vmlab
make install                          # → $GOPATH/bin/vmlab
PREFIX=$HOME/.local make install      # → $HOME/.local/bin/vmlab
```

Pre-built binaries (darwin/linux × amd64/arm64) ship on every tagged
release — grab a tarball from [GitHub Releases](https://github.com/edihasaj/vmlab/releases).

## Quickstart

```sh
# 1. set up dirs and a starter flow
vmlab init

# 2. add a target — local, crabbox, adb, idb, simctl, maestro, abx, guiport
vmlab target add --name dev-mac --transport local --tags local,mac
vmlab target add --name ubuntu-local --transport crabbox --tags linux,vm \
  --set crabbox.id=ubuntu-local

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
| `vmlab instance add/ls/show/rm/status/restart` | Manage provider instances under `~/.vmlab/instances/`. |
| `vmlab up <instance>` | Ensure an instance is running and ready (idempotent). |
| `vmlab down <instance> [--dispose=…]` | Dispose of an instance — `keep\|suspend\|poweroff\|destroy`. |
| `vmlab restart <instance>` | Reboot an instance and wait for ready — recovers a wedged guest agent. |
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
| `ssh` | Direct SSH to Linux hosts (with optional `ssh.display` for X11/Xvfb desktop UI). |
| `ssh-windows` | Windows over SSH — PowerShell + UIAutomation + SendKeys for input verbs. |
| `parallels-guest` | Parallels guest (Windows/macOS) — read-mostly verbs via `prlctl`. |
| `abx` | Headless browser actions. Tagged for `web` capability. |
| `guiport` | Native macOS desktop UI driving via Accessibility + OCR fallback. |
| `adb` | Android devices and AVDs. |
| `idb` | iOS devices via the idb-companion stack. |
| `simctl` | iOS Simulator via `xcrun simctl`. |
| `maestro` | Declarative mobile flows. |

Adding a new transport = ~200 LOC adapter implementing the `Transport`
interface. See [`docs/architecture.md`](docs/architecture.md).

## Scale instances up & down

vmlab doesn't just drive targets that already exist — it provisions them. A
**provider** owns an instance's lifecycle: scale it up, hand back a ready
target, and scale it down afterwards. So one command can spin a cloud VM up,
run the verify loop on it, and tear it back down.

```sh
# one-shot: up → run → restore prior state
vmlab with gpu-burst -- vmlab run gpu-burst flows/verify.yaml

# or drive the lifecycle by hand
vmlab up   gpu-burst                      # scale up: create/boot, wait until ready
vmlab run  gpu-burst flows/verify.yaml
vmlab down gpu-burst --dispose=destroy    # scale down: keep|suspend|poweroff|destroy
```

- **Idempotent.** `up` is a no-op when the instance is already ready; `down`
  honours `only_if_we_started`, so vmlab never suspends a VM you were using.
- **Budget caps.** Set `budget.hourlyUSD` and vmlab refuses to scale up when
  the provider quotes a higher rate — a guard against a misconfigured region
  or instance type.
- **Orphan sweep.** `vmlab orphans --destroy` cleans up any cloud resource
  tagged `vmlab=*` that outlived its run.
- **Snapshots.** Providers that support it expose `vmlab snapshot save/restore`.

| Provider | Backend | Default transport | Scale-down default |
|---|---|---|---|
| `parallels` | `prlctl` (local or over SSH) | `parallels-guest` | suspend |
| `hetzner` | `hcloud` | `ssh` | destroy |
| `aws` | `aws` CLI | `ssh` | destroy |
| `azure` | `az` CLI | `ssh` | destroy |
| `gcp` | `gcloud` | `ssh` | destroy |
| `tart` | `tart` (Apple silicon) | `ssh` | keep |
| `windows` | local / Hyper-V | `ssh-windows` | keep |

Instances live in `~/.vmlab/instances/<name>.yaml`. See
[`docs/providers.md`](docs/providers.md) for the instance schema, host→guest
mounts, snapshots, and per-provider pricing sources.

### Bootstrap a Parallels Linux VM

For a freshly created Ubuntu VM in Parallels, make it vmlab/crabbox-ready from
the host:

```sh
vmlab instance setup-linux \
  --vm "Ubuntu 24.04.3 ARM64" \
  --host 10.211.55.7 \
  --prefix ubuntu \
  --share farm=$HOME/Projects/farm
```

The command creates or reuses `~/.ssh/vmlab_<prefix>`, adds the public key to
the guest, enables SSH + avahi, installs baseline Linux tooling, creates a
writable crabbox work root, configures Parallels shared folders, appends an
SSH host alias with a timestamped backup, and writes repo-local smoke targets
under `vmlab/targets/` plus flows under `vmlab/flows/`.

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

### GUI steps

A `gui:` step dispatches a structured desktop action through the target's
GUI-capable transport (today: `guiport` on macOS). Useful for verifying
desktop apps end-to-end without dropping back to free-form shell.

```yaml
# examples/flows/guiport-e2e.yaml
steps:
  - run: osascript -e 'tell application "TextEdit" to activate'
  - gui: { kind: wait, extra: { milliseconds: 600 } }
  - gui: { kind: observe }
  - gui: { kind: type, text: "hello vmlab" }
  - gui: { kind: screenshot, path: /tmp/shot.png }
  - assert: 'test -s /tmp/shot.png'
```

Supported `kind`s map to guiport verbs: `click`, `click-text`, `click-at`,
`type`, `hotkey`, `screenshot`, `observe`, `tree`, `wait` (host-side sleep),
`run` (replay a guiport YAML). See `examples/flows/guiport-e2e.yaml` for a
runnable demo and `examples/flows/recall-cross-os.yaml` for a cross-OS
flow (linux + windows + mac, single junit.xml).

#### Gating steps on environment

`when:` now accepts `env=NAME` and `env!=NAME` clauses alongside `os=` and
`arch=`. Use this to make a step opt-in via an env var — handy for actions
that need a TCC grant the rest of the flow doesn't:

```yaml
# Only runs when invoked as VMLAB_GUI_SCREENSHOT=1 vmlab run ...
- when: env=VMLAB_GUI_SCREENSHOT
  gui: { kind: screenshot, path: /tmp/shot.png }
- when: env=VMLAB_GUI_SCREENSHOT
  assert: 'test -s /tmp/shot.png'
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

- `$VMLAB_HOME/config.yaml` — user-level defaults when `VMLAB_HOME` is set.
- `~/.vmlab/config.yaml` — user-level defaults otherwise.
- `<repo>/.vmlab.yaml` — repo overrides.
- `$VMLAB_HOME/targets/*.yaml` or `~/.vmlab/targets/*.yaml` — user targets;
  repo `.vmlab/targets/*.yaml` shadow them.
- `$VMLAB_HOME/runs/<run-id>/` or `~/.vmlab/runs/<run-id>/` — evidence
  bundles (kept 30 days by default).

## The agent fleet

vmlab is one corner of an agent fleet. Each ships standalone; vmlab composes
them when you point a target or instance at one. Full diagram in
[`docs/agent-fleet.md`](docs/agent-fleet.md).

| Project | Role |
|---|---|
| [**vmlab**](https://github.com/edihasaj/vmlab) | Cross-OS orchestrator. Transports + flows + evidence bundles. You are here. |
| [**guiport**](https://github.com/edihasaj/guiport) | Local macOS desktop driver. AX + OCR fallback. Standalone CLI/MCP; vmlab's `guiport` transport drives it. |
| [**shotport**](https://github.com/edihasaj/shotport) | Token-cheap screenshot capture for agents. Text first, budgeted pixels only when needed — cheap visual evidence for verify loops. |
| [**recall**](https://github.com/edihasaj/recall) | Local repo-memory compiler for coding agents. vmlab verifies it cross-OS via `examples/flows/recall-cross-os.yaml`. |

## License

MIT. See [LICENSE](LICENSE).

Author: [Edi Hasaj](https://edihasaj.com).
