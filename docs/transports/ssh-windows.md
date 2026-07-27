# Windows over SSH

Use `ssh-windows` for Windows machines with OpenSSH enabled.

```yaml
name: win-azure
transport: ssh-windows
capabilities:
  shell: true
  sync: true
  install: true
  screenshot: true
  gui: true
ssh:
  host: 100.64.0.10
  user: vmlab
  identity: ~/.ssh/vmlab
  shell: powershell
  guiSession: interactive
```

## Interactive desktop

Windows OpenSSH commands do not run in the logged-in user's desktop session.
Set `ssh.guiSession: interactive` to route GUI actions and screenshots through
a temporary Task Scheduler task using the `INTERACTIVE` principal.

Requirements:

1. A user must remain logged in to the Windows desktop.
2. The SSH user must be allowed to create and run scheduled tasks.
3. The desktop must remain unlocked for input and useful screenshots.

For an unattended lab VM, use a dedicated low-privilege test account with
automatic sign-in. Keep production credentials and data off that account.
Restrict RDP and SSH to a private network such as Tailscale.

The interactive task and its staging files are removed after each action. UAC
secure-desktop prompts remain intentionally inaccessible. Pre-bootstrap
required elevated operations instead of attempting to click UAC prompts.

Windows UI Automation providers can be incomplete or blocking, especially
inside WebView2. `click-text` uses an exact Name or AutomationId match and UIA
actions have a short failure bound. Use `click-at` for browser, Electron, and
other WebView surfaces.
