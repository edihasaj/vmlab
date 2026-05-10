# vmlab — goal

**One CLI for agents to install, set up, test, and verify software across any reachable target.**

## Problem

Coding agents (Claude, Codex, opencode, Gemini) can edit code fast but can't easily verify it across real environments. Each target speaks a different protocol:

- SSH for Linux / Windows / macOS VMs
- ADB for Android (devices + AVD)
- idb / xcrun simctl for iOS (devices + Simulator)
- Accessibility APIs for native desktop apps
- Headless browser for web

Today this means 5+ disjoint tools, glue scripts per repo, no shared evidence model, no fan-out, no agent-friendly surface. The verify loop becomes the bottleneck of agentic coding.

## Goal

A single transport-agnostic orchestrator that turns *"install foo, run its smoke suite, capture proof"* into one command — no matter if the target is a Hetzner Linux VM, a Parallels Windows guest, a Pixel phone, an iPhone simulator, a Mac mini, or a ChromeOS box.

## Principles

- **Transport plugins, not transport assumptions.** Every target is a `(transport, address, capabilities)` triple. Adding ChromeOS or a new device class = one adapter, no core changes.
- **Reuse, don't reinvent.** crabbox owns SSH-reachable boxes. guiport owns desktop UI. abx owns web. Maestro / adb / idb own mobile. vmlab orchestrates, never duplicates.
- **Local-first.** Code lives on the dev machine, syncs to the target on each run. No "push to CI to test."
- **Agent-native.** Predictable JSON output, MCP server mode, durable run IDs, evidence bundles (logs + screenshots + timing + JUnit) reviewable by humans and machines.
- **Fleet-friendly.** Tag selectors (`@linux`, `@mobile`, `all`) fan out with streamed, prefixed output. One run, N targets, comparable results.
- **Boring infra.** Single Go binary, brew tap, no daemon. Config in `~/.vmlab/`, per-repo overrides via `.vmlab.yaml`.
- **Composable.** Every subcommand prints JSON with `--json`. Pipeable. Scriptable. No interactive-only paths.

## Non-goals

- **Not a CI replacement.** Pairs with CI; doesn't host pipelines.
- **Not a provisioner.** Doesn't create VMs (crabbox does that for cloud; you provision Parallels / devices yourself).
- **Not a test framework.** Runs your existing scripts and test commands; doesn't define a DSL beyond minimal flow YAML.
- **Not a packaging tool.** Doesn't build, sign, or notarize artifacts.
- **Not a UI driver.** Delegates to guiport / Maestro / abx; never reaches into platform APIs itself.

## Targets at v1

| Class | Transport | Status |
|---|---|---|
| Linux VM (local + cloud) | crabbox ssh / brokered | day-1 |
| Windows VM | crabbox ssh `target:windows` | day-1 |
| macOS host + VM | crabbox ssh + guiport | day-1 |
| Web app | abx | day-1 |
| Android (physical + AVD) | adb (or Maestro) | v1 |
| iOS (physical + sim) | idb + xcrun simctl (or Maestro) | v1 |
| ChromeOS | Crostini ssh / adb | v1.x |

## Success looks like

- `vmlab run all install.sh` walks every configured target, streams output, exits non-zero on any failure, drops a single evidence bundle.
- `vmlab serve --mcp` lets Claude drive the whole fleet through typed tools, no shell glue.
- Adding a new device class = ~200 LOC adapter + one docs page, no core changes.
- The same flow definitions work locally on Mac Studio Parallels and on Hetzner cloud bursts.
- `vmlab doctor` answers *"can I test on every target right now?"* in under 10 seconds.

## Why now

Agentic coding only pays off when the verify loop is as fast as the edit loop. Cross-platform verification is the bottleneck. Every dev with more than one target is rebuilding this glue privately. vmlab is the public, opinionated answer.

## Position vs. neighbors

- **crabbox** — sync + exec on SSH-reachable hosts. vmlab uses it as a transport, doesn't replace it.
- **guiport** — structured desktop UI driving. vmlab uses it as a transport for `gui` actions.
- **abx** — fast headless browser for agents. vmlab uses it as a transport for `web` actions.
- **Maestro** — mobile flow runner. vmlab uses it (or adb/idb directly) as a mobile transport.
- **Appium / WebDriverAgent / Selenium** — heavy, server-based, not agent-first. vmlab stays out of that lane.
- **CI runners (GitHub Actions / Buildkite)** — vmlab is the inner loop; CI is the outer loop. Same flow files run in both.

## License

MIT. Public. Brew tap at `edihasaj/tap/vmlab`.
