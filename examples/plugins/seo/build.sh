#!/usr/bin/env bash
# build.sh — compile the gonext-seo example plugin to seo.wasm.
#
# The plugin is written in Go but built with TinyGo because the
# stock Go toolchain produces oversized WASM (binaries 5-10 MiB) and
# pulls in scheduler/GC routines that don't run inside the wazero
# sandbox. TinyGo cross-compiles to a clean WASI module under 1 MiB.
#
# Usage:
#
#   ./build.sh
#
# Prerequisites:
#
#   - TinyGo >= 0.31.0  (https://tinygo.org/getting-started/install)
#   - Go     >= 1.25.0
#
# Output:
#
#   ./seo.wasm   — the WASM blob referenced by manifest.json's "entry"
#
# Invariants the script enforces after build:
#
#   - The blob exists.
#   - The blob is non-empty.
#   - The blob exports gn_handle_hook, gn_handle_job, gn_alloc, gn_free
#     (sanity-checked by grepping the binary for the symbol names; a
#     real CI would invoke `wasm-tools strip --print-imports` instead,
#     but grep is the lowest-dependency option).
#
# We deliberately do NOT bundle the plugin into a .gnplugin ZIP here —
# the lifecycle Manager accepts a .gnplugin (a ZIP containing the
# manifest + the wasm); building the bundle is a separate concern
# handled by `gonext plugin pack` in the CLI. The build.sh contract is
# just "produce the wasm".

set -euo pipefail

cd "$(dirname "$0")"

OUTPUT=seo.wasm

echo "[build] compiling $OUTPUT with TinyGo..."
if ! command -v tinygo >/dev/null 2>&1; then
  echo "[build] tinygo not on PATH; install from https://tinygo.org/" >&2
  exit 1
fi

# -target=wasi gives us a module that imports the WASI namespace plus
# the env hostmodule the runtime provides. -no-debug strips DWARF; we
# don't want the per-byte cost in the shipped blob (operators who need
# step-debug can rebuild without -no-debug locally).
tinygo build \
  -target=wasi \
  -no-debug \
  -o "$OUTPUT" \
  .

# ---- Invariants -----------------------------------------------------

if [ ! -s "$OUTPUT" ]; then
  echo "[build] FAIL: $OUTPUT was not produced or is empty" >&2
  exit 1
fi

echo "[build] checking required exports..."
required_exports=(gn_handle_hook gn_handle_job gn_alloc gn_free)
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
echo "[build]   zip -j seo.gnplugin manifest.json seo.wasm"
