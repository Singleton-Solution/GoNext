# Regenerate the WASM byte-slice constants in ../testdata_test.go from
# the .wat sources in this directory. Requires `wat2wasm` (from
# `wabt`, https://github.com/WebAssembly/wabt). Without wat2wasm,
# tests still pass — the bytes are vendored in testdata_test.go.
#
# Usage:
#   make            # compile .wat -> .wasm and dump as Go bytes
#   make clean      # remove generated .wasm
#
# The generated .wasm files in this directory are NOT committed to
# git; they are an intermediate artifact for the byte-extraction
# step. The committed source of truth is testdata_test.go.

WATS := $(wildcard *.wat)
WASMS := $(WATS:.wat=.wasm)

.PHONY: all clean check

all: $(WASMS)

%.wasm: %.wat
	wat2wasm $< -o $@

check:
	@which wat2wasm > /dev/null || (echo "wat2wasm not found; install wabt"; exit 1)

clean:
	rm -f $(WASMS)
