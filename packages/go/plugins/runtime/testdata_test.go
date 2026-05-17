package runtime

// Hand-authored WASM binaries for runtime_test.go. Source `.wat` files
// live alongside this file in wat/; see wat/README.md for the format and
// regeneration workflow.
//
// Each byte slice is annotated section-by-section so the format stays
// auditable. The encoding is WebAssembly 1.0 binary format:
//
//   magic    : 00 61 73 6d
//   version  : 01 00 00 00
//   section  : id (u8) + size (LEB128) + payload
//
// LEB128 unsigned: 7 bits per byte, MSB set means "more bytes follow".
// All literal section sizes here fit in a single byte (< 0x80) so the
// LEB128 encoding is indistinguishable from a raw u8. Section sizes
// are computed as the byte-length of the payload that follows the size
// itself (i.e., NOT including the section id or the size byte).

// wasmAdd is the binary form of wat/add.wat:
//
//	(module
//	  (func $add (export "add") (param i32 i32) (result i32)
//	    local.get 0
//	    local.get 1
//	    i32.add))
var wasmAdd = []byte{
	// --- header ---
	0x00, 0x61, 0x73, 0x6d, // magic "\0asm"
	0x01, 0x00, 0x00, 0x00, // version 1

	// --- type section (id=1) ---
	// payload = 0x01 + 0x60 + 0x02 0x7f 0x7f + 0x01 0x7f = 7 bytes
	0x01, 0x07, // id, size
	0x01,             // num types
	0x60,             // func type tag
	0x02, 0x7f, 0x7f, // 2 params, both i32
	0x01, 0x7f, // 1 result, i32

	// --- function section (id=3) ---
	// payload = 0x01 + 0x00 = 2 bytes
	0x03, 0x02, // id, size
	0x01, // num functions
	0x00, // function 0 uses type 0

	// --- export section (id=7) ---
	// payload = 0x01 + 0x03 + "add" + 0x00 + 0x00 = 7 bytes
	0x07, 0x07, // id, size
	0x01,                // num exports
	0x03, 'a', 'd', 'd', // name length + bytes
	0x00, // export kind: function
	0x00, // function index: 0

	// --- code section (id=10) ---
	// body = 0x00 (no locals) + local.get 0 + local.get 1 + i32.add + end = 7 bytes
	// payload = 0x01 (1 body) + 0x07 (body size) + body = 9 bytes
	0x0a, 0x09, // id, size
	0x01,       // num function bodies
	0x07,       // body size
	0x00,       // local decl count
	0x20, 0x00, // local.get 0
	0x20, 0x01, // local.get 1
	0x6a, // i32.add
	0x0b, // end
}

// wasmPanic is the binary form of wat/panic.wat:
//
//	(module
//	  (import "env" "gn_panic" (func $gn_panic (param i32 i32)))
//	  (memory (export "memory") 1)
//	  (data (i32.const 0) "boom from guest")
//	  (func $boom (export "boom")
//	    i32.const 0
//	    i32.const 15
//	    call $gn_panic))
var wasmPanic = []byte{
	// header
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,

	// --- type section ---
	// 2 types:
	//   type 0: (i32, i32) -> ()    used by gn_panic import
	//   type 1: () -> ()            used by boom
	// payload = 0x02 + (0x60 0x02 0x7f 0x7f 0x00) + (0x60 0x00 0x00) = 1+5+3 = 9
	0x01, 0x09,
	0x02,
	0x60, 0x02, 0x7f, 0x7f, 0x00,
	0x60, 0x00, 0x00,

	// --- import section ---
	// 1 import:
	//   module name: 0x03 "env"          = 4 bytes
	//   import name: 0x08 "gn_panic"     = 9 bytes
	//   kind=0x00 (func), type idx=0x00  = 2 bytes
	// entry total = 4 + 9 + 2 = 15
	// payload = 0x01 + entry = 16 = 0x10
	0x02, 0x10,
	0x01,
	0x03, 'e', 'n', 'v',
	0x08, 'g', 'n', '_', 'p', 'a', 'n', 'i', 'c',
	0x00, 0x00,

	// --- function section ---
	// payload = 0x01 + 0x01 = 2
	0x03, 0x02,
	0x01, // num functions
	0x01, // function 0 (boom) uses type 1

	// --- memory section ---
	// payload = 0x01 (num memories) + 0x00 (limits=min only) + 0x01 (min pages) = 3
	0x05, 0x03,
	0x01, 0x00, 0x01,

	// --- export section ---
	// 2 exports. Each entry: name_len(1) + name_bytes + kind(1) + index(1).
	//   "memory" [0x06]memory[0x02][0x00] = 1+6+1+1 = 9
	//   "boom"   [0x04]boom[0x00][0x01]   = 1+4+1+1 = 7
	//     (func index 1 because import counts first → index 0 is gn_panic)
	// payload = 1 (num exports) + 9 + 7 = 17 = 0x11
	0x07, 0x11,
	0x02,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x04, 'b', 'o', 'o', 'm', 0x00, 0x01,

	// --- code section ---
	// body = 0x00 (no locals) + i32.const 0 (2) + i32.const 15 (2) + call 0 (2) + end (1) = 8
	// payload = 1 (num bodies) + 1 (body size byte) + 8 (body) = 10 = 0x0a
	0x0a, 0x0a,
	0x01,
	0x08,
	0x00,
	0x41, 0x00,
	0x41, 0x0f,
	0x10, 0x00,
	0x0b,

	// --- data section ---
	// 1 segment, active, memory 0:
	//   0x00 (memory-idx-flag=0)        = 1
	//   offset expr: 0x41 0x00 0x0b     = 3
	//   byte count: 0x0f                = 1
	//   payload bytes: 15
	//   total per segment: 1+3+1+15 = 20
	// section payload = 0x01 + 20 = 21 = 0x15
	0x0b, 0x15,
	0x01,
	0x00,
	0x41, 0x00, 0x0b,
	0x0f,
	'b', 'o', 'o', 'm', ' ', 'f', 'r', 'o', 'm', ' ', 'g', 'u', 'e', 's', 't',
}

// wasmLog is the binary form of wat/log.wat:
//
//	(module
//	  (import "env" "gn_log" (func $gn_log (param i32 i32 i32)))
//	  (memory (export "memory") 1)
//	  (data (i32.const 0) "hi from plugin")
//	  (func $say_hi (export "say_hi")
//	    i32.const 1
//	    i32.const 0
//	    i32.const 14
//	    call $gn_log))
var wasmLog = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,

	// type section:
	//   type 0: (i32, i32, i32) -> () — encoded as 0x60 0x03 0x7f 0x7f 0x7f 0x00 (6 bytes)
	//   type 1: () -> ()              — encoded as 0x60 0x00 0x00 (3 bytes)
	// payload = 0x02 + 6 + 3 = 10 = 0x0a
	0x01, 0x0a,
	0x02,
	0x60, 0x03, 0x7f, 0x7f, 0x7f, 0x00,
	0x60, 0x00, 0x00,

	// import section:
	//   "env" / "gn_log" / func / type 0
	//   entry: 4 + 7 + 2 = 13
	// payload = 0x01 + 13 = 14 = 0x0e
	0x02, 0x0e,
	0x01,
	0x03, 'e', 'n', 'v',
	0x06, 'g', 'n', '_', 'l', 'o', 'g',
	0x00, 0x00,

	// function section
	0x03, 0x02, 0x01, 0x01,

	// memory section: min 1 page
	0x05, 0x03, 0x01, 0x00, 0x01,

	// export section:
	//   "memory" [0x06]memory[0x02][0x00]   = 1+6+1+1 = 9
	//   "say_hi" [0x06]say_hi[0x00][0x01]   = 1+6+1+1 = 9 (func index 1; gn_log is import 0)
	// payload = 1 (num exports) + 9 + 9 = 19 = 0x13
	0x07, 0x13,
	0x02,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x06, 's', 'a', 'y', '_', 'h', 'i', 0x00, 0x01,

	// code section:
	// body = 0x00 + i32.const 1 (2) + i32.const 0 (2) + i32.const 14 (2) + call 0 (2) + end (1)
	//      = 1+2+2+2+2+1 = 10
	// payload = 0x01 + 0x0a + 10 = 12 = 0x0c
	0x0a, 0x0c,
	0x01,
	0x0a,
	0x00,
	0x41, 0x01,
	0x41, 0x00,
	0x41, 0x0e,
	0x10, 0x00,
	0x0b,

	// data section:
	//   1 segment, mem 0, offset expr 0x41 0x00 0x0b, len 0x0e, 14 bytes
	//   per segment: 1+3+1+14 = 19
	// payload = 0x01 + 19 = 20 = 0x14
	0x0b, 0x14,
	0x01,
	0x00,
	0x41, 0x00, 0x0b,
	0x0e,
	'h', 'i', ' ', 'f', 'r', 'o', 'm', ' ', 'p', 'l', 'u', 'g', 'i', 'n',
}

// wasmTime is the binary form of wat/time.wat:
//
//	(module
//	  (import "env" "gn_time_ms" (func $gn_time_ms (result i64)))
//	  (func $get_time (export "get_time") (result i64)
//	    call $gn_time_ms))
var wasmTime = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,

	// type section: 1 type (() -> i64) shared by import and export
	// 0x60 0x00 0x01 0x7e = 4 bytes
	// payload = 0x01 + 4 = 5
	0x01, 0x05,
	0x01,
	0x60, 0x00, 0x01, 0x7e,

	// import section:
	//   "env" / "gn_time_ms" / func / type 0
	//   entry: 4 + 11 + 2 = 17
	// payload = 0x01 + 17 = 18 = 0x12
	0x02, 0x12,
	0x01,
	0x03, 'e', 'n', 'v',
	0x0a, 'g', 'n', '_', 't', 'i', 'm', 'e', '_', 'm', 's',
	0x00, 0x00,

	// function section
	0x03, 0x02, 0x01, 0x00,

	// export section:
	//   "get_time" 0x08 + 8 + 0x00 0x01 = 11
	// payload = 0x01 + 11 = 12 = 0x0c
	0x07, 0x0c,
	0x01,
	0x08, 'g', 'e', 't', '_', 't', 'i', 'm', 'e',
	0x00, 0x01,

	// code section:
	// body = 0x00 + call 0 (2) + end (1) = 4
	// payload = 0x01 + 0x04 + 4 = 6
	0x0a, 0x06,
	0x01,
	0x04,
	0x00,
	0x10, 0x00,
	0x0b,
}

// wasmConcurrent is the binary form of wat/concurrent.wat:
//
//	(module
//	  (func $square (export "square") (param i32) (result i32)
//	    local.get 0
//	    local.get 0
//	    i32.mul))
var wasmConcurrent = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,

	// type section: (i32) -> i32
	// 0x60 0x01 0x7f 0x01 0x7f = 5 bytes
	// payload = 0x01 + 5 = 6
	0x01, 0x06,
	0x01,
	0x60, 0x01, 0x7f, 0x01, 0x7f,

	// function
	0x03, 0x02, 0x01, 0x00,

	// export:
	//   "square" 0x06 + 6 + 0x00 0x00 = 9
	// payload = 0x01 + 9 = 10 = 0x0a
	0x07, 0x0a,
	0x01,
	0x06, 's', 'q', 'u', 'a', 'r', 'e',
	0x00, 0x00,

	// code:
	// body = 0x00 + local.get 0 (2) + local.get 0 (2) + i32.mul (1) + end (1) = 7
	// payload = 0x01 + 0x07 + 7 = 9
	0x0a, 0x09,
	0x01,
	0x07,
	0x00,
	0x20, 0x00,
	0x20, 0x00,
	0x6c,
	0x0b,
}

// wasmBigMem is the binary form of wat/bigmem.wat — a module with
// (memory 1024) declared, used to verify the runtime's 256-page cap
// (16 MiB) rejects oversized requests.
//
//	(module
//	  (memory (export "memory") 1024)
//	  (func $touch (export "touch")))
//
// 1024 encoded as LEB128 unsigned = 0x80 0x08
// (1024 = 0b00000010_00000000 = top byte 0b0000_1000, low byte 0b1000_0000)
var wasmBigMem = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,

	// type: () -> () = 0x60 0x00 0x00 = 3 bytes
	// payload = 0x01 + 3 = 4
	0x01, 0x04,
	0x01,
	0x60, 0x00, 0x00,

	// function
	0x03, 0x02, 0x01, 0x00,

	// memory: 1 memory, limits flag 0 (only min), min = 1024 (LEB128: 0x80 0x08)
	// payload = 0x01 + 0x00 + 0x80 0x08 = 4
	0x05, 0x04,
	0x01, 0x00, 0x80, 0x08,

	// export:
	//   "memory" 0x06 + 6 + 0x02 0x00 = 9
	//   "touch"  0x05 + 5 + 0x00 0x00 = 8
	// payload = 0x02 + 9 + 8 = 18 = 0x12
	0x07, 0x12,
	0x02,
	0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
	0x05, 't', 'o', 'u', 'c', 'h', 0x00, 0x00,

	// code:
	// body = 0x00 (no locals) + end = 2
	// payload = 0x01 + 0x02 + 2 = 4
	0x0a, 0x04,
	0x01,
	0x02,
	0x00,
	0x0b,
}

// wasmInvalid is a deliberately-malformed binary used to verify that
// malformed bytes are reported as *CompileError rather than panicking.
// The magic bytes are wrong on purpose.
var wasmInvalid = []byte{
	0xde, 0xad, 0xbe, 0xef,
	0x01, 0x00, 0x00, 0x00,
	// random trailing bytes; wazero rejects on magic check first
	0xff, 0xff, 0xff, 0xff,
}
