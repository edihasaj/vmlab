#!/usr/bin/env bash
# vmlab pre-commit hook. Mirrors the CI fmt + vet gates so they fail at the
# commit boundary instead of at push time. Install with `make install-hooks`.
#
# Skip with `git commit --no-verify` when you genuinely need to bypass.
set -euo pipefail

# Only run if there are staged Go files. Cheaper than scanning the tree on
# commits that touch only YAML / Markdown.
staged_go=$(git diff --cached --name-only --diff-filter=ACMR | grep -E '\.go$' || true)
if [ -z "$staged_go" ]; then
  exit 0
fi

# gofmt: list anything that would change. We check the whole tree (not just
# staged files) because CI checks the whole tree — keep parity.
fmt_out=$(gofmt -l .)
if [ -n "$fmt_out" ]; then
  echo "pre-commit: gofmt issues (run 'make fmt'):"
  echo "$fmt_out"
  exit 1
fi

# go vet on the whole module. Fast (<2s on clean cache).
if ! go vet ./...; then
  echo "pre-commit: go vet failed"
  exit 1
fi
