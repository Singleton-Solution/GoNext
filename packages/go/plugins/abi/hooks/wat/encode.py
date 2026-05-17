#!/usr/bin/env python3
"""
Encode the_content.wasm into the Go byte-slice constant the test
binary uses.

Reads `the_content.wasm` from the current directory (where the Makefile
runs this script) and writes a complete `fixturedata_test.go` to
stdout.

Why a generator instead of //go:embed? The repo-level .gitignore
excludes *.wasm. If we used go:embed, the .wasm would need to be
committed in spite of the gitignore. The byte-slice form sidesteps
that.
"""

import sys
from pathlib import Path

WASM = Path("the_content.wasm")

PREAMBLE = '''package hooks

// theContentWasm is the compiled WebAssembly fixture used by the test
// suite. The human-readable source lives in wat/the_content.wat; this
// file is generated from wat2wasm output by wat/encode.py.
//
// Committing the byte slice (rather than the .wasm) keeps `go test`
// free of the repo-level *.wasm gitignore.
//
// To regenerate after editing wat/the_content.wat:
//
//\tmake -C wat
//
// Length: {length} bytes.
var theContentWasm = []byte{{
'''

def main() -> int:
    data = WASM.read_bytes()
    out = [PREAMBLE.format(length=len(data))]
    line = []
    for b in data:
        line.append("0x%02x" % b)
        if len(line) == 12:
            out.append("\t" + ", ".join(line) + ",\n")
            line = []
    if line:
        out.append("\t" + ", ".join(line) + ",\n")
    out.append("}\n")
    sys.stdout.write("".join(out))
    return 0

if __name__ == "__main__":
    raise SystemExit(main())
