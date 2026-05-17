# Test WebAssembly fixtures

This directory holds the `.wat` (WebAssembly text) sources for the
fixtures used by `runtime_test.go`. The actual `.wasm` bytes used by
tests live in `../testdata_test.go` as Go byte-slice constants so the
test binary has no on-disk dependency and `go test` works on any host
without needing `wat2wasm` installed.

The `.wat` files in this directory are the human-authored source of
truth — anyone reviewing the test fixtures can read them here rather
than reverse-engineering the byte slices. The Makefile in this
directory rebuilds the `.wasm` from `.wat` when `wat2wasm` is
available, and a small Go helper (`update_testdata.go`, run via
`go run`) extracts the bytes back into `testdata_test.go`.

## Fixtures

- `add.wat` — exports `add(i32, i32) -> i32`. Used to verify a happy-path
  Call.
- `panic.wat` — imports `env.gn_panic` and exports `boom()`, which calls
  it with a hard-coded message. Used to verify trap classification.
- `log.wat` — imports `env.gn_log` and exports `say_hi()`. Used to verify
  the host log function is wired.
- `time.wat` — imports `env.gn_time_ms` and exports `get_time() -> i64`.
- `concurrent.wat` — exports `square(i32) -> i32`. Used by the race
  test that fires N goroutines at the same module.
- `bigmem.wat` — declares a memory with `(memory 1024)` (64 MiB initial)
  to verify the 16 MiB instantiation cap rejects it.

## Why hand-authored bytes

Building a Go-only build requires WASM bytes that don't depend on a
host toolchain. The hand-authored bytes in `testdata_test.go` are
small (under 200 bytes each), documented section-by-section, and
verified against `wat2wasm` output during development. They are
test-only and never ship in a release artifact.
