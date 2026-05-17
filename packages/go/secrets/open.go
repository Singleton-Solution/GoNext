package secrets

import (
	"fmt"
	"strings"
)

// Open returns a Store described by spec. The spec is a URI-shaped string
// so it survives env vars, YAML, and CLI flags cleanly:
//
//	env:                  EnvStore   — read from process environment
//	file:/run/secrets     FileStore  — read from /run/secrets/<key>.txt
//	noop:                 NoopStore  — every Get returns ErrNotFound
//
// Reserved scheme names — implementation lands in follow-up PRs:
//
//	vault://host/path     VaultStore  (KV v2; not yet implemented)
//	aws-sm://region        AWSSMStore (Secrets Manager; not yet implemented)
//
// Unknown or reserved-but-unimplemented schemes return an error rather
// than silently falling back to env: a misconfigured backend in
// production should fail loud at boot, not pretend everything is fine.
func Open(spec string) (Store, error) {
	scheme, rest, ok := splitScheme(spec)
	if !ok {
		return nil, fmt.Errorf("secrets: spec %q missing scheme (try \"env:\")", spec)
	}
	switch scheme {
	case "env":
		// Anything after "env:" is ignored — there's nothing to configure.
		return NewEnvStore(), nil

	case "file":
		dir := strings.TrimPrefix(rest, "//")
		if dir == "" {
			return nil, fmt.Errorf("secrets: file: scheme needs a directory (e.g. file:/run/secrets)")
		}
		return NewFileStore(dir), nil

	case "noop":
		return NewNoopStore(), nil

	case "vault", "aws-sm":
		return nil, fmt.Errorf("secrets: scheme %q is reserved; adapter not yet implemented", scheme)

	default:
		return nil, fmt.Errorf("secrets: unknown scheme %q (known: env, file, noop)", scheme)
	}
}

// splitScheme returns (scheme, rest, true) for "scheme:rest" inputs and
// (\"\", \"\", false) otherwise. It deliberately doesn't use net/url because
// we want strict scheme parsing without dragging in URL escaping for what
// are mostly local paths.
func splitScheme(spec string) (scheme, rest string, ok bool) {
	i := strings.IndexByte(spec, ':')
	if i <= 0 {
		return "", "", false
	}
	return spec[:i], spec[i+1:], true
}
