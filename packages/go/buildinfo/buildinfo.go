// Package buildinfo exposes build-time metadata about the GoNext binary.
//
// Values are normally injected by the linker via -ldflags at release time;
// at `go run` / `go build` without flags they default to "dev" / "unknown".
//
// Example:
//
//	go build -ldflags "-X github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Version=v0.1.0 \
//	                   -X github.com/Singleton-Solution/GoNext/packages/go/buildinfo.Commit=$(git rev-parse HEAD)"
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Linker-injected variables. Override with -ldflags at build time.
var (
	// Version is the semantic version of the binary, e.g. "v0.1.0".
	Version = "dev"

	// Commit is the git commit SHA the binary was built from.
	Commit = "unknown"

	// Date is the build timestamp (RFC3339).
	Date = "unknown"
)

// Info captures the static identity of the running binary.
type Info struct {
	Service   string `json:"service"`   // e.g. "api", "worker"
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Get returns the build info for the running binary.
// `service` is the logical name of the binary (e.g., "api", "worker", "cli").
func Get(service string) Info {
	commit := Commit
	if commit == "unknown" {
		// Fall back to debug.BuildInfo VCS data if available (set by `go build`
		// from a git checkout without explicit -ldflags).
		if bi, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range bi.Settings {
				if setting.Key == "vcs.revision" && setting.Value != "" {
					commit = setting.Value
					break
				}
			}
		}
	}
	return Info{
		Service:   service,
		Version:   Version,
		Commit:    commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}
