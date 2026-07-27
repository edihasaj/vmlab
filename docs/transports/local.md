# Local

Use `local` when vmlab and the target workload run on the same machine.

```yaml
name: win-azure
transport: local
tags:
  - windows
  - azure
capabilities:
  shell: true
  install: true
  screenshot: true
  gui: true
```

On Windows, a process running in the logged-in interactive session can capture
the desktop and drive UI Automation directly. This makes `local` suitable for
an Azure Windows GUI worker whose vmlab or Probeport process starts through an
interactive current-user Scheduled Task.

The desktop must remain logged in and unlocked for useful screenshots and input.
Local GUI capability is not advertised on macOS or Linux; use `guiport`, `abx`,
or an SSH desktop transport there.
