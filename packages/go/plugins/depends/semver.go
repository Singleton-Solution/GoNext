package depends

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

// matchRange reports whether version satisfies rangeExpr.
//
// The accepted operator vocabulary is the small npm-compatible subset
// the manifest schema allows:
//
//	"^1.2.0"  — same major, version >= 1.2.0 < 2.0.0
//	"~1.2.0"  — same minor, version >= 1.2.0 < 1.3.0
//	">=1.2.0" — minimum
//	"<2.0.0"  — strict maximum
//	">1.2.0"  — strict minimum
//	"<=2.0.0" — maximum
//	"1.2.0"   — exact match
//	"*"       — any version
//	"a b c"   — composite AND (every clause must match)
//
// Compound ranges are space-separated. The "||" union operator is NOT
// supported — the schema doesn't disallow it, but every range in the
// registry today uses ANDs, and rejecting OR up front keeps the
// resolver auditable. If a real OR appears the function returns an
// error so the operator sees a clear "unsupported range" message
// instead of a silent never-matches.
//
// On any malformed range, returns false + a descriptive error. The
// caller (resolver.Check) treats that as an incompatibility — a
// manifest with a junk range can never resolve.
func matchRange(version, rangeExpr string) (bool, error) {
	if version == "" {
		return false, fmt.Errorf("empty version")
	}
	rangeExpr = strings.TrimSpace(rangeExpr)
	if rangeExpr == "" {
		return false, fmt.Errorf("empty range")
	}
	if rangeExpr == "*" {
		// "*" is the universal wildcard. We still require that the
		// caller's version is itself a syntactically valid semver,
		// otherwise the comparison further down would compare junk
		// strings.
		if !semver.IsValid(asV(version)) {
			return false, fmt.Errorf("invalid version %q", version)
		}
		return true, nil
	}
	if strings.Contains(rangeExpr, "||") {
		return false, fmt.Errorf("unsupported || in range %q", rangeExpr)
	}

	v := asV(version)
	if !semver.IsValid(v) {
		return false, fmt.Errorf("invalid version %q", version)
	}

	// Split on whitespace; each clause is one constraint that must
	// pass on its own. Adjacent operators like ">= 1.2.0" (operator
	// and value separated by space) are normalised below.
	clauses := splitClauses(rangeExpr)
	for _, c := range clauses {
		ok, err := matchClause(v, c)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

// splitClauses normalises a range into individual clauses. It collapses
// whitespace so ">=  1.2.0" reads the same as ">=1.2.0", and treats a
// space between an operator and its value as part of the same clause
// (npm-style).
func splitClauses(expr string) []string {
	// First, glue any operator that's followed by whitespace + value
	// back into a single token. We do this with a state machine
	// rather than regexes so the surface area stays auditable.
	var (
		clauses []string
		cur     strings.Builder
		expect  bool // we just saw an operator and need its value
	)
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			clauses = append(clauses, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(expr); i++ {
		ch := expr[i]
		switch ch {
		case ' ', '\t':
			if expect {
				// Don't break the clause yet — we're still gathering
				// the value half of an operator-value pair.
				continue
			}
			flush()
		case '>', '<', '=', '~', '^':
			if cur.Len() > 0 && !expect {
				// A new operator without whitespace ends the previous
				// clause: ">=1.0.0<2.0.0" is two clauses concatenated.
				flush()
			}
			cur.WriteByte(ch)
			expect = true
		default:
			cur.WriteByte(ch)
			expect = false
		}
	}
	flush()
	return clauses
}

// matchClause evaluates one normalised clause (e.g. "^1.2.0",
// ">=1.0.0", "1.2.0") against a v-prefixed semver value.
func matchClause(vWithPrefix, clause string) (bool, error) {
	if clause == "" {
		return false, fmt.Errorf("empty clause")
	}
	switch {
	case strings.HasPrefix(clause, "^"):
		return matchCaret(vWithPrefix, clause[1:])
	case strings.HasPrefix(clause, "~"):
		return matchTilde(vWithPrefix, clause[1:])
	case strings.HasPrefix(clause, ">="):
		return cmpAtLeast(vWithPrefix, clause[2:], 0)
	case strings.HasPrefix(clause, "<="):
		return cmpAtMost(vWithPrefix, clause[2:], 0)
	case strings.HasPrefix(clause, ">"):
		return cmpAtLeast(vWithPrefix, clause[1:], 1)
	case strings.HasPrefix(clause, "<"):
		return cmpAtMost(vWithPrefix, clause[1:], -1)
	case strings.HasPrefix(clause, "="):
		return matchExact(vWithPrefix, clause[1:])
	default:
		return matchExact(vWithPrefix, clause)
	}
}

// matchExact returns true iff the installed version equals base
// exactly (semver canonical form). The match ignores build metadata,
// matching npm's behavior.
func matchExact(vWithPrefix, base string) (bool, error) {
	b := asV(base)
	if !semver.IsValid(b) {
		return false, fmt.Errorf("invalid exact %q", base)
	}
	return semver.Compare(vWithPrefix, b) == 0, nil
}

// cmpAtLeast: vWithPrefix >= base when want=0, vWithPrefix > base
// when want=1. Returns false (no error) on a clean "doesn't match".
func cmpAtLeast(vWithPrefix, base string, want int) (bool, error) {
	b := asV(base)
	if !semver.IsValid(b) {
		return false, fmt.Errorf("invalid bound %q", base)
	}
	c := semver.Compare(vWithPrefix, b)
	if want == 0 {
		return c >= 0, nil
	}
	return c > 0, nil
}

// cmpAtMost: vWithPrefix <= base when want=0, vWithPrefix < base when
// want=-1.
func cmpAtMost(vWithPrefix, base string, want int) (bool, error) {
	b := asV(base)
	if !semver.IsValid(b) {
		return false, fmt.Errorf("invalid bound %q", base)
	}
	c := semver.Compare(vWithPrefix, b)
	if want == 0 {
		return c <= 0, nil
	}
	return c < 0, nil
}

// matchCaret: ^X.Y.Z accepts >=X.Y.Z and <(X+1).0.0 for X>=1, or
// >=0.Y.Z and <0.(Y+1).0 for X=0 (npm "caret-zero" rule).
func matchCaret(vWithPrefix, base string) (bool, error) {
	major, minor, _, err := splitSemver(base)
	if err != nil {
		return false, fmt.Errorf("caret %q: %w", base, err)
	}
	lo := asV(base)
	if !semver.IsValid(lo) {
		return false, fmt.Errorf("invalid caret base %q", base)
	}
	if semver.Compare(vWithPrefix, lo) < 0 {
		return false, nil
	}
	var hi string
	if major == 0 {
		hi = fmt.Sprintf("v0.%d.0", minor+1)
	} else {
		hi = fmt.Sprintf("v%d.0.0", major+1)
	}
	return semver.Compare(vWithPrefix, hi) < 0, nil
}

// matchTilde: ~X.Y.Z accepts >=X.Y.Z and <X.(Y+1).0.
func matchTilde(vWithPrefix, base string) (bool, error) {
	major, minor, _, err := splitSemver(base)
	if err != nil {
		return false, fmt.Errorf("tilde %q: %w", base, err)
	}
	lo := asV(base)
	if !semver.IsValid(lo) {
		return false, fmt.Errorf("invalid tilde base %q", base)
	}
	if semver.Compare(vWithPrefix, lo) < 0 {
		return false, nil
	}
	hi := fmt.Sprintf("v%d.%d.0", major, minor+1)
	return semver.Compare(vWithPrefix, hi) < 0, nil
}

// splitSemver parses major.minor.patch (without the v prefix). It
// tolerates pre-release/build suffixes but doesn't return them — the
// caller only needs the three numeric components to build the
// caret/tilde upper bound.
func splitSemver(s string) (major, minor, patch int, err error) {
	// Strip any prerelease/build suffix first.
	core := s
	if i := strings.IndexAny(core, "-+"); i >= 0 {
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 1 {
		return 0, 0, 0, fmt.Errorf("not a semver: %q", s)
	}
	get := func(idx int) (int, error) {
		if idx >= len(parts) {
			return 0, nil
		}
		n, err := strconv.Atoi(parts[idx])
		if err != nil {
			return 0, fmt.Errorf("not a number: %q", parts[idx])
		}
		if n < 0 {
			return 0, fmt.Errorf("negative: %d", n)
		}
		return n, nil
	}
	if major, err = get(0); err != nil {
		return
	}
	if minor, err = get(1); err != nil {
		return
	}
	if patch, err = get(2); err != nil {
		return
	}
	return major, minor, patch, nil
}

// asV normalises a version string to the "v"-prefixed form that
// golang.org/x/mod/semver requires.
func asV(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "v") || strings.HasPrefix(s, "V") {
		return "v" + s[1:]
	}
	return "v" + s
}
