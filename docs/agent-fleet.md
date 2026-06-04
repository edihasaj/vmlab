# The agent fleet — vmlab + guiport + recall

Three standalone projects, one composed loop. Each is installable on its
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
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│  guiport     │  │   abx        │  │  ssh / ...   │
│ (macOS GUI)  │  │ (web pixels) │  │  (linux/win) │
└──────┬───────┘  └──────────────┘  └───────┬──────┘
       │                                    │
       │  AX + Vision                       │  shell
       │                                    │
       ▼                                    ▼
  Native macOS                        Linux/Windows
   apps                                 VMs
```

## Where the seams live

| Surface | Owned by | Used from vmlab via |
|---|---|---|
| Desktop UI (macOS, incl. Electron) | guiport | `transport: guiport` |
| Desktop UI (Linux, X11/Xvfb, incl. Electron) | xdotool + ImageMagick | `transport: ssh` with `ssh.display` set |
| Desktop UI (Windows, incl. Electron) | PowerShell + UIAutomation + SendKeys | `transport: ssh-windows` (input verbs) or `parallels-guest` (read-only verbs) |
| Mobile (Android) | adb | `transport: adb` |
| Mobile (iOS sim) | simctl / idb / maestro | `transport: simctl|idb|maestro` |
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
