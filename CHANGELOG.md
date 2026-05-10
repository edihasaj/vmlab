# Changelog

All notable changes to vmlab will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added (post-initial)

- JSON-schema validation for target and flow YAML (santhosh-tekuri/jsonschema),
  with line/path errors on bad files.
- `--dry-run` on `vmlab run` — prints the resolved plan (targets + steps).
- `--verbose` / `-v` root flag — enables DEBUG `log/slog` output.
- `junit.xml` written into every evidence bundle; per-step failure
  attribution when running flows.
- Adapter tests with PATH-injected fake binaries for crabbox, adb, idb,
  simctl, maestro, abx, guiport.
- MCP server now uses `github.com/mark3labs/mcp-go` (proper initialise
  handshake, typed tool descriptors, sampling-ready).

### Added

- Cobra-based CLI: `init`, `target`, `doctor`, `run`, `shell`, `web`, `gui`,
  `screenshot`, `evidence`, `serve`, `version`.
- Transport interface with adapters for `crabbox`, `abx`, `guiport`, `adb`,
  `idb`, `simctl`, `maestro`, plus a `local` transport for self-targeting.
- YAML target registry with user (`~/.vmlab/targets/`) + repo (`.vmlab/targets/`)
  layering.
- Tag selector grammar (`all`, `<name>`, `@tag`, `@a,@b`, `not:@tag`, `a;b`).
- Minimal flow YAML (`run` / `assert` steps) executed via the chosen transport.
- Fan-out runner with prefixed streamed output, `--max-parallel`, `--fail-fast`,
  `--continue-on-error`.
- Evidence bundles under `~/.vmlab/runs/<run-id>/` with per-target stdout,
  stderr, step JSON, and `meta.json`. `evidence ls/show/bundle/prune`.
- MCP server (`vmlab serve --mcp`) over stdio with read-only tools by default
  and write-mode tools (`vmlab_run`, `vmlab_web`, `vmlab_gui`) gated by
  `--allow-write`.
- GoReleaser config + Homebrew tap formula.
