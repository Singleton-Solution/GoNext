# Makefile for the sdk-go-hello example plugin.
#
# The default target produces plugin.wasm via TinyGo. The bundle
# target produces a .gnplugin ZIP suitable for `gonext plugin
# install`.
#
# Requirements:
#   - tinygo >= 0.31.0   (https://tinygo.org/getting-started/install)
#   - Go     >= 1.25.0   (for the host-side stock builds)
#   - zip                (for the bundle target)
#
# The build is deliberately straight-line: TinyGo gets the wasi
# target, -no-debug strips DWARF, and the output is the same
# plugin.wasm the manifest.json entry references.

WASM := plugin.wasm
BUNDLE := sdk-go-hello.gnplugin

.PHONY: all clean bundle test check

all: $(WASM)

$(WASM): main.go go.mod
	@command -v tinygo >/dev/null 2>&1 || { \
	  echo "tinygo not on PATH; install from https://tinygo.org/" >&2; exit 1; }
	tinygo build -target=wasi -no-debug -o $(WASM) .
	@echo "[build] $(WASM) ($$(wc -c <$(WASM) | tr -d ' ') bytes)"

# bundle packs manifest + wasm into the .gnplugin ZIP the lifecycle
# Manager accepts. The -j flag flattens paths so the ZIP doesn't
# carry the project directory prefix.
bundle: $(WASM)
	zip -j $(BUNDLE) manifest.json $(WASM)
	@echo "[bundle] $(BUNDLE)"

# test runs the stock-Go tests for the plugin's handler logic. The
# SDK's host-call layer is stubbed under the stock toolchain so
# tests focus on the Go-level handler behaviour.
test:
	go test ./...

# check is the CI gate: compile under both toolchains so a TinyGo-
# only regression doesn't ship.
check: test $(WASM)
	@echo "[check] OK"

clean:
	rm -f $(WASM) $(BUNDLE)
