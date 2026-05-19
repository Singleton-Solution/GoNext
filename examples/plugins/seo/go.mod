// gonext-seo example plugin module.
//
// This is a separate Go module because the TinyGo toolchain works best
// on a module that pulls only what it actually needs (encoding/json,
// strings, unicode). Pulling the host module (packages/go) would drag
// in postgres, redis, asynq, etc. — none of which TinyGo can compile.
//
// The test suite imports the parent's manifest validator through a
// relative replace directive so the manifest used by the example is
// validated against the exact schema the lifecycle Manager runs.
//
// Listed in go.work so `go test ./examples/plugins/seo/...` from the
// repo root resolves the replace target correctly.

module github.com/Singleton-Solution/GoNext/examples/plugins/seo

go 1.25.0

require github.com/Singleton-Solution/GoNext/packages/go v0.0.0

require (
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	golang.org/x/text v0.37.0 // indirect
)

replace github.com/Singleton-Solution/GoNext/packages/go => ../../../packages/go
