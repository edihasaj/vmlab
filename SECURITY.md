# Security Policy

## Reporting a vulnerability

Email **edihasaj@gmail.com** with details. Do not file a public GitHub issue.

Please include:

- A description of the vulnerability.
- Steps to reproduce.
- Affected version/commit.
- Expected vs. observed impact.

You'll get an acknowledgement within 72 hours and a status update within 7 days.

## Threat model

vmlab orchestrates real machines. With targets and instances configured, it can:

- Execute arbitrary commands on remote and local targets over SSH and via vendor CLIs (`crabbox`, `abx`, `guiport`, `adb`, `idb`, `prlctl`, `hcloud`, `aws`, `az`, `gcloud`).
- Create and destroy cloud instances — that has direct **cost and data implications**.
- Run user-authored flow YAML. **Treat flow files like shell scripts:** anyone who can write a flow can drive your machines and your cloud account.

That power is the point — but it means **only run vmlab binaries and flows you trust**.

## Credentials

- Credentials come from the environment or `op://` references, resolved per-step and never persisted to disk.
- Never hardcode secrets, tokens, or hosts in flows, targets, instances, or examples.
- Provider auth (Hetzner/AWS/Azure/GCP/Parallels) uses the vendor CLI's own credential chain — keep those scoped to least privilege.

## Hardening tips

- Run flows against scratch cloud projects, throwaway VMs, or dedicated test accounts when possible.
- Tag everything: `vmlab orphans --destroy` cleans cloud resources tagged `vmlab=*` — untagged resources are invisible to it.
- Review a flow's steps and any scripts it invokes before running it against production targets.
- Don't commit evidence bundles that captured secrets (typed tokens, env dumps) to public repos.

## Supported versions

Pre-1.0 only the latest minor receives fixes.
