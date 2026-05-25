// Package sdk is the TinyGo-targeted Go plugin SDK for GoNext.
//
// This is its own Go module — separate from packages/go — because the
// host module pulls in pgx, redis, asynq, OTel and other deps that
// TinyGo cannot compile. The SDK has zero non-stdlib deps so a plugin
// author runs `tinygo build -target=wasi -o plugin.wasm .` against a
// module whose entire transitive closure fits inside what TinyGo
// supports.
//
// Stdlib usage is limited to encoding/json, errors, strconv, strings,
// sync, and unsafe — every one of which TinyGo handles on the wasi
// target. We never import net/http (the host provides HTTP via
// gn_http_fetch), database/sql (the host provides db via gn_db_read /
// gn_db_write), or any reflect-heavy package.

module github.com/Singleton-Solution/GoNext/packages/go/sdk

go 1.25.0
