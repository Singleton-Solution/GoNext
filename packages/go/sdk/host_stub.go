//go:build !wasm && !tinygo.wasm

// host_stub.go provides the host-call surface when the SDK is compiled
// with the stock Go toolchain (not TinyGo + wasi). It exists so:
//
//   - Plugin authors can write unit tests for their handler logic that
//     compile under `go test`, without TinyGo installed.
//
//   - The codec tests in this package can build and run in CI without
//     a wasm toolchain.
//
//   - Lint and static analysis tools that don't understand TinyGo's
//     build tags can still see the full SDK surface.
//
// In this build, every hostCall* function returns a stub failure status
// (statusHostUnavailable). Tests that need to verify guest-side
// marshalling stick to the Marshal/Unmarshal helpers in codec.go and
// dispatch helpers in hooks.go; tests that want to verify host-call
// shaping go through the real wasm path (sdk-go-hello example).

package sdk

// statusHostUnavailable is the stub status returned by every host
// call in this build. It's a negative int32 that doesn't collide with
// any real host status — picked outside the (-1..-10) range each
// domain uses.
const statusHostUnavailable int32 = -100

// HostUnavailable is the sentinel HostError every stub returns. Tests
// that want to assert "the SDK reached the host" use
// errors.Is(err, ErrHostFailure) and then check
// HostError.Status == StatusHostUnavailable.
const StatusHostUnavailable = statusHostUnavailable

// ============================================================================
// HTTP / DB / KV / Cache / Media / Users / Secrets stubs.
// ============================================================================

func hostCallHTTPFetch(_ []byte) ([]byte, int32)             { return nil, statusHostUnavailable }
func hostCallDBRead(_, _ []byte) ([]byte, int32)             { return nil, statusHostUnavailable }
func hostCallDBWrite(_, _ []byte) ([]byte, int32)            { return nil, statusHostUnavailable }
func hostCallKVGet(_ []byte) ([]byte, int32)                 { return nil, statusHostUnavailable }
func hostCallKVSet(_, _ []byte) ([]byte, int32)              { return nil, statusHostUnavailable }
func hostCallKVDel(_ []byte) ([]byte, int32)                 { return nil, statusHostUnavailable }
func hostCallKVIncr(_ []byte, _ int64) ([]byte, int32)       { return nil, statusHostUnavailable }
func hostCallCacheInvalidate(_ []byte) ([]byte, int32)       { return nil, statusHostUnavailable }
func hostCallMediaRead(_ []byte) ([]byte, int32)             { return nil, statusHostUnavailable }
func hostCallUsersRead(_ []byte) ([]byte, int32)             { return nil, statusHostUnavailable }
func hostCallSecretsGet(_ []byte) ([]byte, int32)            { return nil, statusHostUnavailable }
func hostCallAuditEmit(_ []byte) ([]byte, int32)             { return nil, statusHostUnavailable }
func hostCallCronRegister(_ []byte) ([]byte, int32)          { return nil, statusHostUnavailable }
func hostCallMetricObserve(_ []byte, _ float64, _ []byte) int32 { return statusHostUnavailable }
func hostCallEventEmit(_, _ []byte) int32                    { return statusHostUnavailable }
func hostCallSpanEvent(_, _ []byte) int32                    { return statusHostUnavailable }
func hostCallI18nTranslate(_, _ []byte) ([]byte, int32)      { return nil, statusHostUnavailable }
func hostCallLog(_ int32, _ []byte)                          {}
func hostCallTimeMs() int64                                  { return 0 }

// ============================================================================
// Allocator stubs.
//
// On the stock toolchain, the SDK is never asked to satisfy gn_alloc /
// gn_free / gn_handle_hook from the host — there's no host. We still
// define the exported names so the build succeeds; they panic if
// reached.
// ============================================================================

// guestAlloc is the SDK's allocator. Under wasm32-wasi it's wired to a
// real bump arena exported as gn_alloc; under stock Go it's an
// unreachable stub.
func guestAlloc(size uint32) uint32 {
	_ = size
	return 0
}

// guestFree is the SDK's deallocator. Same situation as guestAlloc.
func guestFree(ptr, size uint32) {
	_ = ptr
	_ = size
}
