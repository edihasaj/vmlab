# transport: ssh-mac

Remote macOS over SSH, with GUI actions delegated to `guiport` on that Mac.

## Capabilities

| Capability | Supported |
|---|---|
| shell | yes |
| sync | yes |
| install | yes |
| gui | yes |
| screenshot | yes |

## Hotkeys and UI Automation

`vmlab gui <target> --kind hotkey --text "cmd+space"` runs remote
`guiport hotkey "cmd+space"` over SSH. This matters for macOS TCC: raw
`vmlab run <target> -- osascript ... key code ...` attributes the automation
to `osascript` / the SSH session and is usually blocked. GUI steps attribute
the action to `guiport`, which can hold the Accessibility grant.

One-time setup on the remote Mac:

1. Install `guiport` where non-login SSH can find it, usually Homebrew
   `/opt/homebrew/bin/guiport` or `/usr/local/bin/guiport`.
2. Grant `guiport` Accessibility in System Settings -> Privacy & Security.
3. For screenshots, also grant Screen Recording. Prefer a signed
   `guiport.app` bundle for durable Screen Recording ownership.

Example:

```sh
vmlab target add --name macbook --transport ssh-mac \
  --set ssh.host=100.x.y.z --set ssh.user=edi --set ssh.port=2222

vmlab gui macbook --kind hotkey --text "option+`"
vmlab gui macbook --kind type --text "hello from vmlab"
vmlab screenshot macbook /tmp/macbook.png
```

System TCC approval still needs a human Touch ID/password step. vmlab can open
and poll the local grant flow with `vmlab grant`; remote Mac grants currently
need to be completed on that Mac's logged-in GUI session.
