# runbook: first run on a fresh Mac

```sh
brew tap edihasaj/tap
brew install vmlab

vmlab init
vmlab target add --name dev-mac --transport local --tags local,mac
vmlab target add --name ubuntu-local --transport crabbox --tags linux,vm \
  --set crabbox.configPath=~/.crabbox/ubuntu-local.yaml

vmlab doctor
# TARGET                   TRANSPORT  OK  MESSAGE
# dev-mac                  local      yes local
# ubuntu-local             crabbox    yes crabbox doctor exit=0

vmlab run @linux flows/install.yaml --max-parallel 4
```

If `vmlab doctor` reports an unhealthy target, the message tells you which CLI
is missing on PATH (`crabbox`, `abx`, `guiport`, `adb`, `idb`, `xcrun`,
`maestro`). Install the missing tool, re-run doctor, and you're back in business.
