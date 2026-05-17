# Regenerate the embedded WASM byte slice from the .wat sources in
# this directory. Requires `wat2wasm` (from `wabt`,
# https://github.com/WebAssembly/wabt) and python3 for the bytes-to-Go
# step.
#
# Usage:
#   make            # compile .wat -> .wasm AND regenerate
#                   # ../fixturedata_test.go from the bytes
#   make wasm       # only compile to .wasm (intermediate)
#   make clean      # remove generated .wasm
#
# The generated .wasm files in this directory are NOT committed to
# git (the repo-level .gitignore excludes *.wasm). The committed
# source of truth is ../fixturedata_test.go, which the encode step
# writes from the compiled bytes.

WATS := $(wildcard *.wat)
WASMS := $(WATS:.wat=.wasm)

.PHONY: all wasm clean check

all: ../fixturedata_test.go

wasm: $(WASMS)

%.wasm: %.wat
	wat2wasm $< -o $@

# Regenerate fixturedata_test.go from the_content.wasm. Each .wat
# corresponds to one Go byte slice; the generator iterates them.
../fixturedata_test.go: the_content.wasm
	python3 ./encode.py > $@

check:
	@which wat2wasm > /dev/null || (echo "wat2wasm not found; install wabt"; exit 1)
	@which python3 > /dev/null || (echo "python3 not found"; exit 1)

clean:
	rm -f $(WASMS)
