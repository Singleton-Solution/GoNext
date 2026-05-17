// Command gonext is the administrative CLI for a GoNext install.
//
// Status: skeleton — issue #1. Subsequent issues add subcommands per
// docs/05-admin-api.md §3.9 (e.g., `gonext plugin install`,
// `gonext migrate`, `gonext jobs`, `gonext bench`).
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Singleton-Solution/GoNext/cli/gonext/cmd/migrate"
	"github.com/Singleton-Solution/GoNext/cli/gonext/cmd/plugin"
	"github.com/Singleton-Solution/GoNext/cli/gonext/cmd/theme"
	"github.com/Singleton-Solution/GoNext/packages/go/buildinfo"
)

func main() {
	args := os.Args[1:]
	switch {
	case len(args) == 0, args[0] == "version", args[0] == "--version", args[0] == "-v":
		info := buildinfo.Get("cli")
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(info); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case args[0] == "help", args[0] == "--help", args[0] == "-h":
		fmt.Println(usage)
	case args[0] == "theme":
		os.Exit(theme.Run(args[1:], os.Stdout, os.Stderr))
	case args[0] == "plugin":
		os.Exit(plugin.RunOS(args[1:]))
	case args[0] == "migrate":
		os.Exit(migrate.RunOS(args[1:]))
	default:
		fmt.Fprintf(os.Stderr, "gonext: unknown command %q\n\n%s\n", args[0], usage)
		os.Exit(2)
	}
}

const usage = `gonext — administrative CLI for a GoNext install

Status: skeleton. Subcommands land in subsequent issues.

Usage:
  gonext <command> [args]

Commands (planned):
  plugin     Manage plugins (install, activate, list, dev, test)
  theme      Manage themes (install, activate, test)
  migrate    Run database migrations or import from WordPress
  jobs       Inspect and manage background jobs (queue, failed, drain, cron)
  bench      Run synthetic performance benchmarks
  version    Print version information

Available now:
  migrate     Apply / roll back / inspect database migrations
  plugin test Run the plugin contract checks against a bundle
  theme test  Run the theme contract suite against a theme on disk
  version     Print build info`
