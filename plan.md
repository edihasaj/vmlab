# vmlab — plan

Implementation plan. Phased, shippable each phase.

## Stack

- **Language:** Go 1.23+ (single static binary; matches crabbox; great concurrency for fan-out).
- **CLI framework:** `spf13/cobra` (command tree, autocompletion).
- **Config:** YAML via `goccy/go-yaml`; schema validation via `santhosh-tekuri/jsonschema`.
- **MCP:** `mark3labs/mcp-go` for `vmlab serve --mcp`.
- **Logging:** `log/slog` with prefixed multi-target output.
- **Release:** GoReleaser → GitHub Releases → Homebrew tap (`edihasaj/homebrew-tap`).
- **CI:** GitHub Actions — fmt, vet, race tests, coverage gate, docs link check, release on tag.

## Repo layout

```
~/Projects/vmlab/
├── cmd/vmlab/main.go
├── internal/
│   ├── config/             # schema, loaders, merging (~/.vmlab + repo .vmlab.yaml)
│   ├── target/             # target registry, tags, selectors
│   ├── transport/          # interface + adapters
│   │   ├── transport.go    # Transport interface
│   │   ├── crabbox.go
│   │   ├── guiport.go
│   │   ├── abx.go
│   │   ├── adb.go
│   │   ├── idb.go
│   │   ├── simctl.go
│   │   └── maestro.go
│   ├── flow/               # YAML flow loader + step runner
│   ├── fleet/              # fan-out, streaming mux, result aggregation
│   ├── evidence/           # bundle (logs + screenshots + timing + junit)
│   ├── doctor/             # health checks per transport + per target
│   └── mcp/                # MCP server mode
├── docs/
│   ├── architecture.md
│   ├── transports/
│   │   ├── crabbox.md
│   │   ├── guiport.md
│   │   ├── abx.md
│   │   ├── adb.md
│   │   ├── idb.md
│   │   └── maestro.md
│   ├── flows.md
│   ├── mcp.md
│   └── runbooks/
├── examples/
│   ├── targets/
│   └── flows/
├── Formula/                # homebrew (autogen by tap)
├── .github/workflows/
├── goreleaser.yaml
├── go.mod
├── README.md
├── goal.md
├── plan.md
├── CHANGELOG.md
└── LICENSE
```

## Core abstractions

### Target

```yaml
# ~/.vmlab/targets/ubuntu-local.yaml
name: ubuntu-local
transport: crabbox
tags: [linux, local, vm]
crabbox:
  configPath: ~/.crabbox/ubuntu-local.yaml   # or inline static.host/user/port
capabilities:
  - shell
  - sync
  - install
  - screenshot: false
  - gui: false
```

### Transport interface

```go
type Transport interface {
    Name() string
    Doctor(ctx context.Context, t Target) Health
    Sync(ctx context.Context, t Target, src string) error
    Run(ctx context.Context, t Target, cmd []string, w io.Writer) (Result, error)
    Shell(ctx context.Context, t Target) error
    Screenshot(ctx context.Context, t Target, path string) error  // optional
    GUI(ctx context.Context, t Target, action GUIAction) error    // optional
    Capabilities() Caps
}
```

Adapters shell out to the underlying tool (`crabbox`, `adb`, `idb`, `guiport`, `abx`, `maestro`). No SDK reimplementation. Doctor verifies each binary on PATH.

### Flow

```yaml
# flows/install.yaml
name: install
steps:
  - run: ./scripts/setup.sh
  - run: ./scripts/install.sh
  - assert: ./scripts/verify.sh
```

Flows are intentionally minimal: shell steps + assert steps. Anything beyond that goes in your own script.

### Selector

```
ubuntu-local              # exact name
@linux                    # tag
@linux,@vm                # AND
all                       # everything
not:@mobile               # exclusion
```

## Phases

### Phase 0 — scaffold (½ day)

- `go mod init github.com/edihasaj/vmlab`
- Cobra command tree skeleton: `init`, `target`, `doctor`, `run`, `shell`, `version`
- Config loader (~/.vmlab/ + repo `.vmlab.yaml`)
- README + goal.md + plan.md committed
- CI: fmt + vet + test scaffold
- GoReleaser snapshot config

**Done when:** `vmlab --help` lists commands; `vmlab version` prints; CI green.

### Phase 1 — crabbox transport, single target (1 day)

- `internal/transport/crabbox.go` — shells to `crabbox run/ssh/doctor`
- `vmlab target add` writes YAML; `vmlab target ls` reads it
- `vmlab doctor` checks crabbox PATH + per-target reachability
- `vmlab run <target> <cmd...>` end-to-end on one Ubuntu VM
- `vmlab shell <target>` interactive

**Done when:** `vmlab run ubuntu-local -- uname -a` works against a real Parallels VM.

### Phase 2 — flows + evidence (1 day)

- Flow YAML loader + step runner
- `vmlab run <target> <flow.yaml>` executes steps
- Evidence bundling: per-run dir under `~/.vmlab/runs/<run-id>/` with stdout, stderr, timing.json, exit codes
- `vmlab evidence <run-id>` prints summary; `--bundle` zips it
- `--json` flag on all read commands

**Done when:** failing step produces inspectable bundle; passing run too.

### Phase 3 — fan-out + selectors (1 day)

- Tag selectors parser
- Concurrent runner with prefixed streamed output (`[ubuntu] ...`, `[fedora] ...`)
- Aggregate exit code (any failure → non-zero)
- `--max-parallel N`, `--fail-fast`, `--continue-on-error`
- `vmlab run @linux <flow>` works

**Done when:** 3 Linux VMs in Parallels run the same flow concurrently with clean output.

### Phase 4 — abx + guiport transports (1 day)

- `internal/transport/abx.go` — proxies abx subcommands; `vmlab web <target> <abx-flow>`
- `internal/transport/guiport.go` — same for guiport; `vmlab gui <target> <guiport-flow>`
- Doctor checks abx + guiport on PATH
- Examples in `examples/flows/`

**Done when:** one web flow + one mac desktop flow execute via vmlab.

### Phase 5 — mobile transports (2 days)

- `internal/transport/adb.go` — Android: `vmlab run <android> install foo.apk` etc.
- `internal/transport/idb.go` + `simctl.go` — iOS device + simulator
- Optional: `internal/transport/maestro.go` — wraps Maestro YAML for mobile flows
- Tag conventions: `@android`, `@ios`, `@mobile`
- Doctor: device list, simulator list, idb companion status

**Done when:** install + smoke flow runs on physical Pixel + iOS simulator from one `vmlab run @mobile install.yaml`.

### Phase 6 — MCP mode (1 day)

- `vmlab serve --mcp` over stdio
- Tools exposed: `vmlab_run`, `vmlab_shell` (capability-gated), `vmlab_doctor`, `vmlab_targets`, `vmlab_evidence`, `vmlab_gui`, `vmlab_web`
- Auth: none for stdio (Claude Code launches it locally)
- Permissions: read-only by default, `--allow-write` flag for shell/run

**Done when:** Claude Code can drive a 3-target fan-out flow end-to-end via MCP, no Bash glue.

### Phase 7 — release (½ day)

- GoReleaser cross-compile (darwin/linux/amd64+arm64)
- Homebrew tap formula auto-bump on tag
- `brew tap edihasaj/tap && brew install vmlab` works
- v0.1.0 tagged; CHANGELOG written

**Done when:** fresh Mac can `brew install vmlab` + `vmlab init` + run flow on Hetzner brokered VM.

### Phase 8 — ChromeOS + polish (later)

- ChromeOS Crostini SSH (works via crabbox already; just adds doctor + tag)
- ChromeOS adb mode (Android subsystem)
- `vmlab usage` summary across runs
- `vmlab attach <run-id>` for live tail
- `vmlab cancel <run-id>`

## Decisions to lock before phase 0

- **Name:** `vmlab` (placeholder; rename if collision found on brew + GitHub before tag).
- **Module path:** `github.com/edihasaj/vmlab`.
- **License:** MIT.
- **Min Go:** 1.23.
- **Min macOS for guiport adapter:** matches guiport (13+).

## Open questions

- **Maestro vs. raw adb/idb?** Maestro = simpler flows but extra dep. Raw = no deps, more LOC. *Likely both: raw adapters for primitive ops, Maestro adapter for declarative flows.*
- **Evidence retention?** Default 30 days; configurable via `~/.vmlab/config.yaml`. Same shape as crabbox runs.
- **Auth across transports?** Each transport owns its own creds (crabbox token, ADB key, idb pairing, guiport AX permission). vmlab never stores creds itself.
- **Repo-level vs user-level targets?** Both: user-level under `~/.vmlab/targets/`, repo-level under `.vmlab/targets/` overrides. Standard XDG-ish merge.

## Risks

- **Tool drift.** Adapters shell to external CLIs whose flags change. Mitigation: pin minimum versions in doctor; integration smoke test in CI uses Docker'd or mocked CLIs.
- **Flow YAML scope creep.** Tempting to add conditionals, retries, env interpolation. Mitigation: keep YAML to `run`/`assert`; push complexity into your shell scripts.
- **Mobile fragmentation.** Android version skew, iOS simulator quirks. Mitigation: doctor surfaces target version; flows can declare min OS.
- **Cross-tool naming clashes.** crabbox has `run`, abx has `goto`, guiport has `click`. vmlab should *not* shadow these — use higher-level verbs (`run`, `web`, `gui`) and pass through.

## First commit checklist

- [ ] `go.mod`, `cmd/vmlab/main.go`, root cobra command
- [ ] `goal.md`, `plan.md`, `README.md` (stub), `LICENSE` (MIT)
- [ ] `.github/workflows/ci.yml` (fmt, vet, test)
- [ ] `.gitignore` (Go)
- [ ] `goreleaser.yaml` (snapshot config, no publish yet)
- [ ] First tag: `v0.0.1` snapshot only

Then proceed to Phase 1.
