# Convenience build target for the sdk-rust-hello example.
#
# The cargo build invocation pins wasm32-wasip1 (the modern alias for
# wasm32-wasi; older toolchains accept either). The output wasm goes
# into `target/wasm32-wasip1/release/sdk_rust_hello.wasm` and is
# copied into the local dir so the bundle is one directory.

TARGET ?= wasm32-wasip1
WASM := target/$(TARGET)/release/sdk_rust_hello.wasm

.PHONY: build clean bundle test

build:
	cargo build --target $(TARGET) --release

bundle: build
	cp $(WASM) ./sdk_rust_hello.wasm

clean:
	cargo clean
	rm -f sdk_rust_hello.wasm

test:
	cargo test
