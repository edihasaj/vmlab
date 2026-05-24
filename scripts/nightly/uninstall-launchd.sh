#!/usr/bin/env bash
# Remove the nightly cross-OS verify launchd agent.
set -euo pipefail

LABEL="com.edihasaj.vmlab.nightly"
DEST="$HOME/Library/LaunchAgents/$LABEL.plist"

if [[ -f "$DEST" ]]; then
    launchctl unload -w "$DEST" 2>/dev/null || true
    rm -f "$DEST"
    echo "removed: $DEST"
else
    echo "not installed (no plist at $DEST)"
fi
