// Package verify is the post-import fidelity check for the WordPress
// importer (packages/go/migrate/importer).
//
// After a WXR import completes, an operator needs an answer to a
// single question: "did the data actually make it across?" The
// importer's own Report tells you how many rows it tried to write,
// not whether the resulting database faithfully represents the
// source. This package walks the source WXR a second time and
// compares each record against what the DB now contains, producing
// a fidelity score the operator can gate on.
//
// Typical usage (from the CLI, see cli/gonext/cmd/migrate/verify.go):
//
//	v := verify.Verifier{
//	    DB: pool,
//	    SourceReader: func() (io.Reader, error) {
//	        return os.Open(filePath)
//	    },
//	}
//	report, err := v.Run(ctx)
//	if err != nil {
//	    return err
//	}
//	if ok, gateErr := (verify.Gate{MinFidelity: 0.95}).Decide(report); !ok {
//	    return gateErr
//	}
//
// The checks are split across files by record kind (posts.go,
// terms.go, comments.go, users.go, permalinks.go) so a future
// importer for a different source format can reuse the comparators
// it cares about and skip the rest. Each check emits Failure rows
// onto the Report; the aggregate fidelity score is
//
//	Fidelity = Passed / ChecksTotal
//
// where Passed = ChecksTotal - Failed. A check that does not run
// (because, e.g., the WXR has no comments) does not contribute to
// either total — the score reflects what was actually verifiable.
//
// See issue #218 and docs/08-migration-compat.md §9.
package verify
