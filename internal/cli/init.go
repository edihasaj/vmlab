// Init wires a new repo for vmlab. It is intentionally agent-friendly:
//   - sniffs the project type (Node / Rust / Python / Go / Swift) and writes a
//     starter flow shaped for it, so an LLM can copy-paste the result.
//   - drops fully-commented example targets and instances under .vmlab/, one
//     per transport / provider, so the YAML surface is discoverable without
//     leaving the repo.
//   - appends a "## vmlab" section to AGENTS.md (or creates one) so agents
//     pick up the local conventions on next session start.
//
// Every write is idempotent: rerunning `vmlab init` only writes files that
// don't exist yet. `--force` overwrites; useful when bumping examples after a
// vmlab upgrade.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/edihasaj/vmlab/internal/config"
	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var force bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Initialise vmlab dirs and write a starter .vmlab.yaml in the current repo",
		Long: `Scaffold a vmlab-ready repo.

Writes:
  - .vmlab.yaml                    repo-level overrides (commented)
  - flows/install.yaml             starter flow shaped for the detected project
  - .vmlab/targets/example.*.yaml  fully-commented target examples per transport
  - .vmlab/instances/example.*.yaml fully-commented instance examples per provider
  - AGENTS.md                      appended with a "## vmlab" section for agents

Idempotent. Re-run to add anything missing without touching what's there. Use
--force to overwrite the scaffold (project sniff result will be re-applied).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, p, err := config.Load()
			if err != nil {
				return err
			}
			if err := config.EnsureDirs(p); err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "user dir:      %s\n", p.UserDir)
			fmt.Fprintf(out, "targets dir:   %s\n", p.TargetDir[0])
			fmt.Fprintf(out, "instances dir: %s\n", p.InstanceDir[0])
			fmt.Fprintf(out, "runs dir:      %s\n", p.RunsDir)

			kind, flowBody := detectProject(cwd)
			fmt.Fprintf(out, "detected:      %s\n", kind)

			for _, s := range scaffolds(cwd, flowBody) {
				if err := writeScaffold(out, s, force); err != nil {
					return err
				}
			}
			if err := ensureAgentsSection(out, cwd, force); err != nil {
				return err
			}
			fmt.Fprintln(out, "")
			fmt.Fprintln(out, "next: vmlab transport ls   # see every transport + capability")
			fmt.Fprintln(out, "      vmlab provider ls    # see every VM provider")
			fmt.Fprintln(out, "      vmlab schema target  # JSON schema for target YAML")
			fmt.Fprintln(out, "      vmlab doctor         # confirm tools + targets are reachable")
			return nil
		},
	}
	c.Flags().BoolVar(&force, "force", false, "overwrite existing files")
	return c
}

// scaffoldFile is one file the scaffold writes. Rel is relative to cwd so
// output lines stay short.
type scaffoldFile struct {
	Rel     string
	Content string
}

func scaffolds(cwd, flowBody string) []scaffoldFile {
	return []scaffoldFile{
		{".vmlab.yaml", repoConfigTemplate},
		{"flows/install.yaml", flowBody},
		{".vmlab/targets/example.local.yaml", targetExampleLocal},
		{".vmlab/targets/example.ssh.yaml", targetExampleSSH},
		{".vmlab/targets/example.ssh-windows.yaml", targetExampleSSHWindows},
		{".vmlab/targets/example.parallels-guest.yaml", targetExampleParallelsGuest},
		{".vmlab/targets/example.adb.yaml", targetExampleADB},
		{".vmlab/targets/example.simctl.yaml", targetExampleSimctl},
		{".vmlab/targets/example.crabbox.yaml", targetExampleCrabbox},
		{".vmlab/instances/example.parallels.yaml", instanceExampleParallels},
		{".vmlab/instances/example.hetzner.yaml", instanceExampleHetzner},
		{".vmlab/instances/example.aws.yaml", instanceExampleAWS},
		{".vmlab/instances/example.tart.yaml", instanceExampleTart},
	}
}

func writeScaffold(out io.Writer, s scaffoldFile, force bool) error {
	path := s.Rel
	if !filepath.IsAbs(path) {
		cwd, _ := os.Getwd()
		path = filepath.Join(cwd, s.Rel)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil && !force {
		fmt.Fprintf(out, "skip: %s (exists; --force to overwrite)\n", s.Rel)
		return nil
	}
	if err := os.WriteFile(path, []byte(s.Content), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote: %s\n", s.Rel)
	return nil
}

// detectProject sniffs the cwd for well-known build manifests and returns a
// short kind name plus a starter flow tailored to it. Falls back to a generic
// flow when nothing matches so init still produces something useful.
func detectProject(cwd string) (kind, flow string) {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(cwd, name))
		return err == nil
	}
	switch {
	case exists("package.json"):
		// Pick the lockfile that's there so the flow installs with the right
		// package manager instead of guessing.
		switch {
		case exists("pnpm-lock.yaml"):
			return "node (pnpm)", flowNodePnpm
		case exists("yarn.lock"):
			return "node (yarn)", flowNodeYarn
		default:
			return "node (npm)", flowNodeNpm
		}
	case exists("Cargo.toml"):
		return "rust", flowRust
	case exists("pyproject.toml") || exists("requirements.txt"):
		return "python", flowPython
	case exists("go.mod"):
		return "go", flowGo
	case exists("Package.swift"):
		return "swift", flowSwift
	}
	return "generic", flowGeneric
}

// ensureAgentsSection appends a "## vmlab" section to AGENTS.md (or creates
// the file). If the marker is already present the function is a no-op unless
// --force is set; in that case the entire section is replaced.
func ensureAgentsSection(out io.Writer, cwd string, force bool) error {
	path := filepath.Join(cwd, "AGENTS.md")
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	body := string(existing)
	if strings.Contains(body, agentsSectionMarker) && !force {
		fmt.Fprintln(out, "skip: AGENTS.md (already has ## vmlab section)")
		return nil
	}
	if strings.Contains(body, agentsSectionMarker) && force {
		// Replace from the marker to the next top-level heading (or EOF).
		body = stripExistingSection(body)
	}
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += "\n" + agentsSection + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	if existing == nil {
		fmt.Fprintln(out, "wrote: AGENTS.md")
	} else {
		fmt.Fprintln(out, "wrote: AGENTS.md (appended ## vmlab section)")
	}
	return nil
}

// stripExistingSection removes the previously-written ## vmlab block so we
// can rewrite it cleanly. The block ends at the next `## ` heading or EOF.
func stripExistingSection(body string) string {
	idx := strings.Index(body, agentsSectionMarker)
	if idx < 0 {
		return body
	}
	tail := body[idx:]
	// Find the next top-level heading after our section header.
	nl := strings.Index(tail, "\n")
	if nl < 0 {
		return body[:idx]
	}
	after := tail[nl+1:]
	if next := strings.Index(after, "\n## "); next >= 0 {
		return body[:idx] + after[next+1:]
	}
	return body[:idx]
}

const agentsSectionMarker = "## vmlab"

const agentsSection = `## vmlab — cross-platform build / test orchestration

This repo uses [vmlab](https://github.com/edihasaj/vmlab) to drive flows across
local hosts, VMs, mobile devices, and browsers. Everything is YAML; the schema
is self-describing — agents can introspect without leaving the shell.

Discover:

` + "```sh" + `
vmlab transport ls            # every transport (ssh, ssh-windows, adb, idb, parallels-guest, …)
vmlab provider ls             # every VM provider (parallels, hetzner, aws, azure, gcp, tart)
vmlab schema target           # JSON schema for target YAML
vmlab schema flow             # JSON schema for flow YAML
vmlab schema instance         # JSON schema for instance YAML
vmlab doctor                  # confirms binaries + target reachability
` + "```" + `

Layout:

- ` + "`flows/*.yaml`" + ` — what to run (sync / run / assert / exec / artifact / when).
- ` + "`.vmlab/targets/*.yaml`" + ` — repo-level targets (a Linux box, a Pixel, an iOS sim).
- ` + "`.vmlab/instances/*.yaml`" + ` — repo-level VM/cloud instances managed by vmlab.
- ` + "`~/.vmlab/{targets,instances}/`" + ` — user-level versions; repo files override.
- ` + "`example.*.yaml`" + ` next to each — fully-commented templates per transport / provider.

Common moves:

` + "```sh" + `
vmlab run @linux flows/install.yaml            # against every @linux target
vmlab with my-vm -- vmlab run my-vm flows/x.yaml # bring up VM, run, restore
vmlab matrix run @ci flows/build.yaml          # cross-OS table output, ND-JSON rows
vmlab watch @ci flows/build.yaml --src .       # re-run on save
` + "```" + `

MCP for agents: ` + "`vmlab serve --mcp --allow-write`" + ` exposes targets, doctor,
evidence, run, web, gui as MCP tools.
`

const repoConfigTemplate = `# .vmlab.yaml — repo-level overrides for vmlab.
# User defaults live in ~/.vmlab/config.yaml. Repo file wins.
#
# evidenceRetentionDays: 30
# defaultMaxParallel: 4
# runsDir: ./.vmlab-runs   # override where evidence bundles land
`

// ---------- starter flows, per detected project type ----------

const flowGeneric = `# flows/install.yaml — minimal starter flow.
# Each step is shell-only by design; push complexity into your scripts.
# Step kinds:
#   run    : execute a command (treated as a step that must exit 0)
#   assert : same as run but framed as a check
#   exec   : like run, but on a specified target (string or list)
#   sync   : sync source files into a target before later steps
#   artifact: build on host, ship binary into target, run smoke
#   when   : gate a step on os= or transport= (e.g. when: os=windows)
name: install
steps:
  - run: echo "hello from vmlab"
  - assert: test -e ./README.md
`

const flowNodePnpm = `# flows/install.yaml — Node + pnpm starter.
name: install
steps:
  - sync: .
  - run: pnpm install --frozen-lockfile
  - run: pnpm build
  - assert: node -e "console.log('node smoke ok')"
`

const flowNodeYarn = `# flows/install.yaml — Node + yarn starter.
name: install
steps:
  - sync: .
  - run: yarn install --frozen-lockfile
  - run: yarn build
  - assert: node -e "console.log('node smoke ok')"
`

const flowNodeNpm = `# flows/install.yaml — Node + npm starter.
name: install
steps:
  - sync: .
  - run: npm ci
  - run: npm run build --if-present
  - assert: node -e "console.log('node smoke ok')"
`

const flowRust = `# flows/install.yaml — Rust starter.
# Use artifact: to build on the host and ship the binary into the target
# (great for cross-compile to Windows / Linux without setting up Cargo there).
name: install
steps:
  - sync: .
  - run: cargo build --release
  - assert: ./target/release/$(basename $(pwd)) --version || true
`

const flowPython = `# flows/install.yaml — Python (uv) starter.
name: install
steps:
  - sync: .
  - run: uv sync
  - run: uv run pytest -q
`

const flowGo = `# flows/install.yaml — Go starter.
name: install
steps:
  - sync: .
  - run: go build ./...
  - run: go test ./...
`

const flowSwift = `# flows/install.yaml — Swift starter.
name: install
steps:
  - sync: .
  - run: swift build
  - run: swift test
`

// ---------- target examples, one per transport ----------

const targetExampleLocal = `# Rename and remove the .disabled suffix to activate, or copy this file to
# ~/.vmlab/targets/ for a user-level target. Run ` + "`vmlab target ls`" + ` to confirm.
name: dev-mac
transport: local
tags: [local, mac]
capabilities:
  shell: true
# 'local' has no settings — it shells out on the dev machine itself.
`

const targetExampleSSH = `name: ubuntu-vm
transport: ssh
tags: [linux, vm]
capabilities:
  shell: true
  sync: true
  install: true
ssh:
  host: 10.0.0.42
  user: ubuntu
  port: "22"
  identity: ~/.ssh/id_ed25519
  strictHost: "accept-new"  # 'yes' | 'no' | 'accept-new'
  # knownHosts: ~/.ssh/known_hosts
  # dest: /home/ubuntu/app  # default sync destination
`

const targetExampleSSHWindows = `name: win11
transport: ssh-windows
tags: [windows, vm]
capabilities:
  shell: true
  sync: true
  install: true
  screenshot: true
ssh:
  host: 10.0.0.99
  user: Administrator
  port: "22"
  identity: ~/.ssh/id_ed25519
  shell: pwsh        # pwsh | powershell | cmd | none
  strictHost: "accept-new"
  # dest: C:/vmlab   # default sync destination on the Windows guest
`

const targetExampleParallelsGuest = `# parallels-guest runs commands via 'prlctl exec' on the host that owns the VM.
# Useful when you don't want to open OpenSSH-Server on the guest. SSH-windows
# is usually preferable for Windows because Sync over rsync is much faster.
name: parallels-win
transport: parallels-guest
tags: [windows, parallels]
capabilities:
  shell: true
  sync: true
parallels:
  vm: "Windows 11"        # prlctl name
  # host: studio.local    # remote Mac running Parallels — SSH'd into
  # user: edi             # user on that remote Mac
`

const targetExampleADB = `name: pixel-7
transport: adb
tags: [android, mobile]
capabilities:
  shell: true
  sync: true
  install: true
  mobile: true
  screenshot: true
adb:
  serial: "RFNX001"   # adb devices
  dest: /sdcard/vmlab # default sync destination on the device
`

const targetExampleSimctl = `name: ios-sim
transport: simctl
tags: [ios, mobile, simulator]
capabilities:
  install: true
  mobile: true
  screenshot: true
simctl:
  udid: "AAA-BBB-CCC"   # xcrun simctl list devices
`

const targetExampleCrabbox = `# crabbox is an external SSH manager — delegate auth/host bookkeeping to it.
name: ubuntu-local
transport: crabbox
tags: [linux, vm]
capabilities:
  shell: true
  sync: true
crabbox:
  configPath: ~/.crabbox/ubuntu-local.yaml
  # name: ubuntu-local   # alternative to configPath
`

// ---------- instance examples, one per provider ----------

const instanceExampleParallels = `# Parallels Desktop instance. ` + "`vmlab up <name>`" + ` resumes it, ` + "`vmlab down`" + `
# suspends / poweroffs per the disposition. Snapshots and shared folders are
# first-class. Pair this with a parallels-guest or ssh-windows target.
name: win11-studio
provider: parallels
tags: [windows, parallels]
parallels:
  vm: "Windows 11"
  host: studio.local        # remote Mac running Parallels (optional)
  user: edi
disposition:
  on_success: suspend       # keep | suspend | poweroff | destroy
  on_failure: keep
  only_if_we_started: true
ready:
  probe: ["powershell.exe", "-NoProfile", "-Command", "$true"]
  timeout: 120s
mounts:
  - name: repo
    host: ./
    from_laptop: true       # rsync laptop → studio cache before sharing
    guest: "Z:\\\\repo"
`

const instanceExampleHetzner = `# Hetzner Cloud — needs HCLOUD_TOKEN or hetzner.token (op:// supported).
name: ci-linux
provider: hetzner
tags: [linux, cloud]
hetzner:
  serverType: cpx21
  image: ubuntu-24.04
  location: nbg1
  sshKeys: [my-key]
  # token: "op://Personal/hcloud/token"
disposition:
  on_success: destroy
  on_failure: destroy
`

const instanceExampleAWS = `name: ci-amazonlinux
provider: aws
tags: [linux, cloud]
aws:
  instanceType: t4g.small
  image: ami-0xxxxxxxxxxxxxxxx
  region: eu-central-1
  keyName: my-key
  securityGroup: sg-xxxxxxxx
  subnet: subnet-xxxxxxxx
disposition:
  on_success: destroy
`

const instanceExampleTart = `# Tart — Apple Silicon micro-VMs. Great for macOS-on-macOS CI.
name: mac-ci
provider: tart
tags: [mac, ci]
tart:
  image: ghcr.io/cirruslabs/macos-sequoia-base:latest
  cpu: 4
  memory: 8G
disposition:
  on_success: poweroff
`
