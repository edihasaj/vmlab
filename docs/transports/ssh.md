# transport: ssh

Plain OpenSSH client. The canonical transport for cloud Linux boxes
(Hetzner, EC2, GCE…) once the provider has emitted a Target pointing at the
running instance.

## Settings

```yaml
name: smoke-arm
transport: ssh
tags: [linux, cloud]
ssh:
  host: 203.0.113.7        # required
  user: root               # default: root
  port: 22                 # optional
  identity: ~/.ssh/id_ed25519
  knownHosts: ~/.ssh/known_hosts       # optional, pins host keys
  strictHost: yes          # yes | accept-new | no (default: yes)
  multiplex: true          # reuse one SSH master socket (default: true)
  controlPersist: 10m      # how long the master lingers idle (default: 10m)
```

`strictHost: accept-new` is the right setting for freshly-provisioned cloud
boxes — first contact records the host key, subsequent connections verify
against it.

### Connection multiplexing (default on)

vmlab spawns the system `ssh` binary per `run`/`cp`/`doctor`. Without reuse,
each spawn pays a full TCP + SSH handshake (often 1–2s, and far worse when the
host throttles new connections). With OpenSSH **ControlMaster** the first
connection opens a persistent master socket under `~/.vmlab/cm/` (or
`$VMLAB_HOME/cm`) and every later connection to the same host rides it — just a
new channel — cutting per-call latency ~4–5×. It is the transport-layer
analogue of keeping one browser/CDP session warm.

This is **enabled by default** for the `ssh`, `ssh-mac`, and `ssh-windows`
transports. The master exits on its own `controlPersist` after the last channel
closes. To disable for a target, set `multiplex: false`. To drop a warm master
manually: `ssh -o ControlPath=~/.vmlab/cm/%C -O exit <user>@<host>`.

## Capabilities

| Capability | Supported |
|---|---|
| shell | yes (interactive ssh) |
| sync | yes (scp -r) |
| install | yes |
| screenshot | no |
| gui | no |

## Doctor

Runs `ssh … -- true` against the configured host. Non-zero exit or missing
`ssh` binary surfaces as unhealthy.

## Quoting

`Run` accepts `[]string` and quotes each element via `posixQuote` before
joining into a single remote command line. Pass commands as separate
arguments; don't pre-quote.
