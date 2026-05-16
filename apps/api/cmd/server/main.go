// Command server is the GoNext HTTP API server.
//
// Status: skeleton — issue #1. Subsequent issues add the HTTP router,
// Postgres connection, auth, the plugin host, and the rest of the stack.
// See ROADMAP.md for the phase ordering.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
)

func main() {
	info := buildinfo.Get("api")
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(info); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
