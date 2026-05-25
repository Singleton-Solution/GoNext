# Makefile for the {{PLUGIN_NAME}} plugin.
#
# Default target produces plugin.wasm via TinyGo. The bundle target
# produces a .gnplugin ZIP suitable for `gonext plugin install`.
#
# Requirements:
#   tinygo >= 0.31.0   (https://tinygo.org/getting-started/install)
#   Go     >= 1.25.0
#   zip                (for the bundle target)

WASM   := plugin.wasm
BUNDLE := {{PLUGIN_NAME}}.gnplugin

.PHONY: all clean bundle test

all: $(WASM)

$(WASM): main.go go.mod
	@command -v tinygo >/dev/null 2>&1 || { \
	  echo "tinygo not on PATH; install from https://tinygo.org/" >&2; exit 1; }
	tinygo build -target=wasi -no-debug -o $(WASM) .
	@echo "[build] $(WASM) ($$(wc -c <$(WASM) | tr -d ' ') bytes)"

bundle: $(WASM)
	zip -j $(BUNDLE) manifest.json $(WASM)
	@echo "[bundle] $(BUNDLE)"

# test runs the stock-Go tests for handler logic. The SDK stubs out
# host calls under the stock toolchain so unit tests focus on the
# Go-level behaviour.
test:
	go test ./...

clean:
	rm -f $(WASM) $(BUNDLE)
