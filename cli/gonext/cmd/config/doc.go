// Package config implements `gonext config` subcommands.
//
// The current entry is `gonext config dump`, an operator-facing helper
// that prints the effective configuration with secrets masked. It's the
// "what did this process actually load" answer to production boot
// failures — short of attaching a debugger.
//
// See packages/go/config.Dump for the redaction rules and output format.
package config
