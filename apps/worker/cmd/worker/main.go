// Command worker is the GoNext background-job runner (Asynq consumer).
//
// Status: skeleton — issue #1. Subsequent issues wire it to Redis,
// the task registry, the WASM plugin host, and the cron leader-election
// lease. See docs/12-jobs-cron.md and ADR 0010.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
)

func main() {
	info := buildinfo.Get("worker")
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(info); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
