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
```

`strictHost: accept-new` is the right setting for freshly-provisioned cloud
boxes — first contact records the host key, subsequent connections verify
against it.

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
