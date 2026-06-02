#!/usr/bin/env bash
# Smoke test for the planned `parallels` provider in vmlab.
#
# Proves the lifecycle end-to-end against a real Parallels guest:
#   resume -> wait-for-tools -> exec commands -> capture evidence -> suspend
#
# Run from your laptop. Talks to a remote Mac host via SSH, then drives the
# guest via `prlctl exec` (Parallels Tools must be installed in the guest).
#
# Usage:
#   scripts/smoke-parallels.sh [host] [vm-name]
#
# Defaults: host=mac-studio.local  vm=Windows 11

set -uo pipefail

HOST="${1:-mac-studio.local}"
VM="${2:-Windows 11}"
RUN_ID="$(date -u +%Y%m%dT%H%M%S)-parallels-smoke"
EV_DIR="${HOME}/.vmlab/runs/${RUN_ID}"
PRL_PATH='/Applications/Parallels Desktop.app/Contents/MacOS'
SSH_OPTS=(-o ConnectTimeout=8 -o BatchMode=yes -o RequestTTY=no)

mkdir -p "${EV_DIR}/guest"
LOG="${EV_DIR}/smoke.log"

EXIT_RC=0
START_STATUS=""
RESUMED=0

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*" | tee -a "$LOG"; }

cleanup() {
  if [ "$RESUMED" = "1" ]; then
    log "cleanup: suspending VM (we resumed it)"
    rprl suspend "\"${VM}\"" 2>&1 | tee -a "$LOG" || EXIT_RC=$((EXIT_RC|2))
    rprl status "\"${VM}\"" >"${EV_DIR}/status-after.txt" 2>&1 || true
    log "final status: $(cat "${EV_DIR}/status-after.txt" 2>/dev/null || echo unknown)"
  else
    log "cleanup: leaving VM as-is (was already running at start)"
  fi
  log "writing meta.json (exit=${EXIT_RC})"
  cat >"${EV_DIR}/meta.json" <<EOF
{
  "run_id": "${RUN_ID}",
  "kind": "parallels-smoke",
  "host": "${HOST}",
  "vm": "${VM}",
  "start_status": "${START_STATUS}",
  "resumed_by_us": ${RESUMED},
  "exit": ${EXIT_RC},
  "finished_at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}
EOF
  exit "$EXIT_RC"
}
trap cleanup EXIT
fail() { log "FAIL: $*"; EXIT_RC=1; exit 1; }

# Remote prlctl wrapper. Quotes the VM name once, here.
rprl() {
  ssh "${SSH_OPTS[@]}" "$HOST" "PATH=\"\$PATH:${PRL_PATH}\" prlctl $*"
}

# Run a command inside the guest. Pass the full guest invocation as ONE arg —
# embedded double quotes are preserved across ssh -> remote shell -> prlctl exec.
gexec() {
  local label="$1"; local guest_cmd="$2"
  local out="${EV_DIR}/guest/${label}.txt"
  log "guest \$ ${guest_cmd}"
  if ssh "${SSH_OPTS[@]}" "$HOST" \
      "PATH=\"\$PATH:${PRL_PATH}\" prlctl exec \"${VM}\" ${guest_cmd}" \
      >"$out" 2>&1; then
    head -3 "$out" | sed 's/^/    /' | tee -a "$LOG"
    return 0
  else
    local rc=$?
    log "    (exit=$rc, see ${out})"
    return $rc
  fi
}

log "== vmlab parallels smoke =="
log "host=${HOST}  vm=${VM}  run-id=${RUN_ID}"
log "evidence dir: ${EV_DIR}"

log "step 1/5: probe ssh + prlctl on host"
rprl --version >"${EV_DIR}/prlctl-version.txt" 2>&1 \
  || fail "prlctl not reachable on ${HOST}"
head -1 "${EV_DIR}/prlctl-version.txt" | sed 's/^/    /' | tee -a "$LOG"

log "step 2/5: capture initial VM status"
START_STATUS="$(rprl status "\"${VM}\"" 2>&1 | tr -d '\r')"
echo "$START_STATUS" | tee -a "$LOG"
echo "$START_STATUS" >"${EV_DIR}/status-before.txt"

case "$START_STATUS" in
  *running*) log "already running — will not change power state on exit" ;;
  *suspended*|*stopped*|*paused*)
    log "resuming VM"
    rprl start "\"${VM}\"" 2>&1 | tee -a "$LOG" \
      || fail "prlctl start failed"
    RESUMED=1
    ;;
  *) fail "unexpected status: ${START_STATUS}" ;;
esac

log "step 3/5: wait for Parallels Tools (poll prlctl exec for up to 120s)"
ready=0
for i in $(seq 1 30); do
  if rprl exec "\"${VM}\"" cmd.exe /c ver >/dev/null 2>&1; then
    ready=1
    log "    tools ready after ~$((i*4))s"
    break
  fi
  sleep 4
done
[ "$ready" = "1" ] || fail "guest tools never became ready"

log "step 4/4: run guest probes (end-to-end via Parallels Tools)"
gexec ver      'cmd.exe /c ver'                                            || fail "ver"
gexec hostname 'cmd.exe /c hostname'                                       || fail "hostname"
gexec whoami   'cmd.exe /c whoami'                                         || fail "whoami"
gexec ipconfig 'cmd.exe /c ipconfig'                                       || fail "ipconfig"
gexec date     'powershell.exe -NoProfile -Command "Get-Date -Format o"'   || fail "date"
gexec osver    'powershell.exe -NoProfile -Command "[Environment]::OSVersion.VersionString"' || fail "osver"

log "== smoke passed =="
# cleanup trap handles suspend + meta.json
