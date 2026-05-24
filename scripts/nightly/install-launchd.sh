#!/usr/bin/env bash
# Install the nightly cross-OS verify as a launchd agent.
#
# Renders scripts/nightly/com.edihasaj.vmlab.nightly.plist.tmpl with the
# user's actual paths, drops it in ~/Library/LaunchAgents, and loads it.
# Idempotent: re-running unloads-then-loads the same label.
set -euo pipefail

LABEL="com.edihasaj.vmlab.nightly"
REPO="$(cd "$(dirname "$0")/../.." && pwd)"
TMPL="$REPO/scripts/nightly/$LABEL.plist.tmpl"
DEST="$HOME/Library/LaunchAgents/$LABEL.plist"
LOG_DIR="$HOME/Library/Logs/vmlab"

if [[ ! -f "$TMPL" ]]; then
    echo "template not found: $TMPL" >&2
    exit 1
fi

# Default vmlab binary: $REPO/bin/vmlab if present (release build), otherwise
# whatever's on PATH. Caller can override with VMLAB_BIN=/path/to/vmlab.
VMLAB_BIN="${VMLAB_BIN:-}"
if [[ -z "$VMLAB_BIN" ]]; then
    if [[ -x "$REPO/bin/vmlab" ]]; then
        VMLAB_BIN="$REPO/bin/vmlab"
    elif command -v vmlab >/dev/null 2>&1; then
        VMLAB_BIN="$(command -v vmlab)"
    else
        echo "vmlab binary not found; run \`make build\` first or set VMLAB_BIN" >&2
        exit 1
    fi
fi

PATH_DEFAULT="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"
PATH_VAR="${VMLAB_NIGHTLY_PATH:-$PATH_DEFAULT}"

mkdir -p "$(dirname "$DEST")" "$LOG_DIR"

sed -e "s|__VMLAB_BIN__|$VMLAB_BIN|g" \
    -e "s|__REPO__|$REPO|g" \
    -e "s|__LOG_DIR__|$LOG_DIR|g" \
    -e "s|__PATH__|$PATH_VAR|g" \
    -e "s|__HOME__|$HOME|g" \
    "$TMPL" > "$DEST"

# Reload if previously loaded; ignore "Could not find" on first run.
launchctl unload -w "$DEST" 2>/dev/null || true
launchctl load -w "$DEST"

cat <<EOF
installed: $DEST
runs:      03:00 local daily
binary:    $VMLAB_BIN
working:   $REPO
logs:      $LOG_DIR/nightly.{out,err}.log

Trigger an immediate test run:
  launchctl start $LABEL

Inspect the next-run time:
  launchctl print gui/\$UID/$LABEL | grep next_run

Uninstall:
  $REPO/scripts/nightly/uninstall-launchd.sh
EOF
