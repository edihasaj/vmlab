# The agent fleet — vmlab + guiport + um + recall

Four standalone projects, one composed loop. Each is installable on its
own; vmlab is the connective tissue.

## What each one does

```
┌───────────────────────────────────────────────────────────────────────┐
│                       Agent (Claude / Codex / …)                      │
│                                                                       │
│         MCP client over stdio  ────────────┐                          │
└──────────────────────┬─────────────────────┴──────────────────────────┘
                       │
                       ▼
            ┌──────────────────────┐
            │  vmlab serve --mcp   │   ← cross-OS orchestrator
            │  vmlab run @<sel>    │     transports + flows + evidence
            └────┬────────┬────────┘
                 │        │
   ┌─────────────┘        └──────────────────────────────────┐
   │                                                         │
   ▼                                                         ▼
┌──────────────┐  ┌────────────────┐  ┌──────────────┐  ┌──────────────┐
│  guiport     │  │  undermouse    │  │   abx        │  │  ssh / ...   │
│ (macOS GUI)  │  │ um act/context │  │ (web pixels) │  │  (linux/win) │
└──────┬───────┘  └────────┬───────┘  └──────────────┘  └───────┬──────┘
       │                   │                                    │
       │  AX + Vision      │  LLM + safe action runtime         │  shell
       │                   │  (delegates clicks to guiport)     │
       ▼                   ▼                                    ▼
  Native macOS         Native macOS                       Linux/Windows
   apps                 apps + LLM                          VMs
```

## Where the seams live

| Surface | Owned by | Used from vmlab via |
|---|---|---|
| Desktop AX/clicks/screenshots (macOS) | guiport | `transport: guiport` |
| LLM context capture + safe action plans | undermouse (`um` CLI) | `transport: undermouse` |
| Headless browser / web pixels | abx (Playwright) | `transport: abx` |
| Linux/Windows shell | ssh / crabbox / parallels-guest | `transport: ssh|crabbox|parallels-guest` |
| Coding agent memory | recall | not a transport — installed *on* targets and verified by `examples/flows/recall-cross-os.yaml` |

vmlab itself doesn't replace any of these. It exposes them under one CLI,
one MCP server, one evidence bundle.

## Workflows the fleet unlocks

**Install + verify recall on a fresh fleet**
```sh
vmlab up lin-vmlab            # Azure Linux comes up
vmlab run @@vm examples/flows/recall-cross-os.yaml --max-parallel 3
```
→ one `~/.vmlab/runs/<run-id>/` with junit.xml across mac + linux + windows.

**Screenshot a web dashboard without macOS TCC**
```sh
vmlab run recall-web examples/flows/recall-web-screenshot.yaml
```
→ abx (Playwright Chromium) captures pixels; the macOS Screen Recording
grant is never asked for. See [`examples/flows/recall-web-screenshot.yaml`](../examples/flows/recall-web-screenshot.yaml).

**Drive UnderMouse's safe action runtime from a flow**
```sh
vmlab run mac-local-um examples/flows/um-smoke.yaml
```
→ `um context` captures AX + frontmost app, `um ask` streams an LLM reply
into evidence, `gui:click` round-trips through `um act --plan`. The
underlying click is still guiport — UnderMouse just wraps it with a
confirmation gate.

**Grant TCC once, agentically (when you need pixel-level capture on macOS)**
```sh
vmlab grant guiport screen-recording
```
→ opens the right Privacy pane, you Touch ID once, vmlab polls
`guiport doctor` until the scope flips to trusted, then returns 0.

## How independence is preserved

- Each project has its own repo, CLI, tests, releases.
- vmlab depends on the others only via PATH + structured CLI surface
  (`guiport observe`, `um act --plan -`, `abx screenshot`, etc.). No
  Go imports cross the project boundaries.
- A user can install just `guiport` and ignore vmlab — guiport stays a
  perfectly capable standalone tool.
- Conversely, vmlab degrades gracefully when one of the tools is missing
  — `vmlab doctor` reports the gap, flows that need the missing tool
  skip with `when:` or fail loudly with a clear error.

## Where to go next

- Add a target/instance — `vmlab init` writes the starter set; otherwise
  see [`examples/targets/`](../examples/targets).
- Write a flow — see [`docs/flows.md`](flows.md) for the YAML schema.
- Wire your agent — [`docs/mcp.md`](mcp.md) covers the MCP surface.
