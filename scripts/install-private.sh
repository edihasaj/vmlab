#!/usr/bin/env bash
# install-private.sh — download + install vmlab from the private GitHub
# release (the repo is private; brew tap is deferred until either the
# repo flips public or a HOMEBREW_TAP_TOKEN is wired in CI — see
# .goreleaser.yaml for the toggle).
#
# Uses your local `gh auth` to fetch the latest release tarball, extracts
# it, and installs the binary into $PREFIX/bin (default /usr/local).
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/edihasaj/vmlab/main/scripts/install-private.sh | bash
#   ./scripts/install-private.sh                 # from a clone
#   PREFIX=$HOME/.local ./scripts/install-private.sh   # no sudo
#   VMLAB_VERSION=v0.2.0 ./scripts/install-private.sh  # pin
set -euo pipefail

REPO="edihasaj/vmlab"
PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="$PREFIX/bin"
VERSION="${VMLAB_VERSION:-}"

if ! command -v gh >/dev/null 2>&1; then
    echo "gh CLI required (brew install gh; gh auth login)" >&2
    exit 1
fi
if ! gh auth status >/dev/null 2>&1; then
    echo "gh is not authenticated. Run: gh auth login" >&2
    exit 1
fi

case "$(uname -s)" in
    Darwin)  os="darwin" ;;
    Linux)   os="linux" ;;
    *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
    arm64|aarch64) arch="arm64" ;;
    x86_64|amd64)  arch="x86_64" ;;
    *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac

if [[ -z "$VERSION" ]]; then
    VERSION="$(gh release view --repo "$REPO" --json tagName --jq .tagName)"
    if [[ -z "$VERSION" ]]; then
        echo "could not determine latest release tag" >&2
        exit 1
    fi
fi
VERSION_NO_V="${VERSION#v}"
ASSET="vmlab_${VERSION_NO_V}_${os}_${arch}.tar.gz"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "==> fetching $ASSET from $REPO@$VERSION"
gh release download "$VERSION" --repo "$REPO" --pattern "$ASSET" --dir "$tmp" >/dev/null

tar -xzf "$tmp/$ASSET" -C "$tmp"
mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/vmlab" "$BIN_DIR/vmlab"

echo "installed: $BIN_DIR/vmlab"
"$BIN_DIR/vmlab" --version || true
