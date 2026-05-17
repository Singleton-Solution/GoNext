package pool

// wasmAdd is a copy of the same byte slice that lives in
// runtime/testdata_test.go. We don't want the pool package's tests to
// import the runtime package's internal test data, so we duplicate
// the bytes here. The format is the WebAssembly 1.0 binary encoding
// of:
//
//	(module
//	  (func $add (export "add") (param i32 i32) (result i32)
//	    local.get 0
//	    local.get 1
//	    i32.add))
//
// See packages/go/plugins/runtime/wat/add.wat for the source and
// packages/go/plugins/runtime/testdata_test.go for the section-by-
// section annotated copy. We re-export the byte literal verbatim;
// regenerate from wat/add.wat via `wat2wasm` if the source changes.
var wasmAdd = []byte{
	// header: magic + version 1
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	// type section
	0x01, 0x07,
	0x01,
	0x60,
	0x02, 0x7f, 0x7f,
	0x01, 0x7f,
	// function section
	0x03, 0x02,
	0x01,
	0x00,
	// export section
	0x07, 0x07,
	0x01,
	0x03, 'a', 'd', 'd',
	0x00,
	0x00,
	// code section
	0x0a, 0x09,
	0x01,
	0x07,
	0x00,
	0x20, 0x00,
	0x20, 0x01,
	0x6a,
	0x0b,
}
