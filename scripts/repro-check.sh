#!/usr/bin/env bash
# Reproducible build check (FR-S08): build every command twice in separate caches with the release flags
# and compare SHA-256. Usage: scripts/repro-check.sh [goos/goarch ...]  (default: host + linux/amd64 + windows/arm64)
set -euo pipefail
cd "$(dirname "$0")/.."
targets=("$@")
[ ${#targets[@]} -eq 0 ] && targets=("$(go env GOOS)/$(go env GOARCH)" linux/amd64 windows/arm64)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fail=0
for t in "${targets[@]}"; do
  for cmd in continuum controller agent; do
    for run in 1 2; do
      GOCACHE="$work/cache$run" CGO_ENABLED=0 GOOS=${t%/*} GOARCH=${t#*/} \
        go build -trimpath -ldflags "-s -w -buildid= -X continuum/internal/version.Version=0.0.0-repro" \
        -o "$work/$run-$cmd" "./cmd/$cmd"
    done
    a=$(shasum -a 256 "$work/1-$cmd" | cut -d' ' -f1)
    b=$(shasum -a 256 "$work/2-$cmd" | cut -d' ' -f1)
    if [ "$a" = "$b" ]; then echo "reproducible  $t  $cmd  ${a:0:16}"; else echo "DIFFERENT     $t  $cmd  ${a:0:16} ${b:0:16}"; fail=1; fi
  done
done
exit $fail
