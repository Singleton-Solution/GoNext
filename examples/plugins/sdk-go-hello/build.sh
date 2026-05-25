#!/usr/bin/env bash
# build.sh — compile the sdk-go-hello example to plugin.wasm via TinyGo.
#
# This mirrors examples/plugins/seo/build.sh but builds against the
# packages/go/sdk wrapper instead of the raw ABI. The output is a
# wasm32-wasi module the GoNext lifecycle Manager accepts.
#
# Usage:
#
#   ./build.sh
#
# Prerequisites:
#
#   - TinyGo >= 0.31.0
#   - Go     >= 1.25.0
#
# Output:
#
#   ./plugin.wasm
#
# After build, the script verifies the binary exports the required
# ABI symbols (gn_handle_hook, gn_alloc, gn_free) and is non-empty.

set -euo pipefail

cd "$(dirname "$0")"

OUTPUT=plugin.wasm

echo "[build] compiling $OUTPUT with TinyGo..."
if ! command -v tinygo >/dev/null 2>&1; then
  echo "[build] tinygo not on PATH; install from https://tinygo.org/" >&2
  exit 1
fi

tinygo build \
  -target=wasi \
  -no-debug \
  -o "$OUTPUT" \
  .

if [ ! -s "$OUTPUT" ]; then
  echo "[build] FAIL: $OUTPUT was not produced or is empty" >&2
  exit 1
fi

echo "[build] checking required exports..."
required_exports=(gn_handle_hook gn_alloc gn_free)
missing=0
for sym in "${required_exports[@]}"; do
  if ! grep -aq "$sym" "$OUTPUT"; then
    echo "[build] FAIL: missing export $sym" >&2
    missing=1
  fi
done
if [ "$missing" -ne 0 ]; then
  exit 1
fi

size_bytes=$(wc -c <"$OUTPUT" | tr -d ' ')
echo "[build] OK: $OUTPUT ($size_bytes bytes)"
echo "[build] To pack as a .gnplugin bundle:"
echo "[build]   zip -j sdk-go-hello.gnplugin manifest.json plugin.wasm"
