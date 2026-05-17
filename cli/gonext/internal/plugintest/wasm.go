package plugintest

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// wasmMagic is the 4-byte WebAssembly module magic ("\0asm"), per the
// WebAssembly spec §5.5.1 (https://webassembly.github.io/spec/core/binary/modules.html#binary-magic).
var wasmMagic = [4]byte{0x00, 0x61, 0x73, 0x6d}

// wasmVersion is the only Wasm binary format version we recognize (v1, the
// MVP). Per the spec the version is little-endian u32 = 1.
const wasmVersion uint32 = 1

// maxWASMSize is the cap declared in docs/02-plugin-system.md §2.1
// ("WASM module size cap: **20 MB**"). The contract test enforces it early
// to give a clear diagnostic before the host tries to instantiate.
const maxWASMSize = 20 * 1024 * 1024

// ValidateWASMHeader does a read-only sanity check on a WebAssembly module:
// non-empty, within the §2.1 size cap, magic bytes match, and version is the
// MVP (1). Full bytecode validation is the runtime's job — this is just
// enough to catch "you committed a text file" before we hand it to wazero.
func ValidateWASMHeader(wasm []byte) error {
	if len(wasm) == 0 {
		return errors.New("wasm: empty module")
	}
	if len(wasm) > maxWASMSize {
		return fmt.Errorf("wasm: module is %d bytes; exceeds %d-byte cap (docs/02-plugin-system.md §2.1)", len(wasm), maxWASMSize)
	}
	if len(wasm) < 8 {
		return fmt.Errorf("wasm: module is %d bytes; too short to contain header", len(wasm))
	}
	var magic [4]byte
	copy(magic[:], wasm[:4])
	if magic != wasmMagic {
		return fmt.Errorf("wasm: bad magic %x; want %x (not a WebAssembly module?)", magic[:], wasmMagic[:])
	}
	v := binary.LittleEndian.Uint32(wasm[4:8])
	if v != wasmVersion {
		return fmt.Errorf("wasm: binary format version %d; want %d", v, wasmVersion)
	}
	return nil
}
