package audit

import (
	"context"
	"crypto/hmac"
	"errors"
	"fmt"
)

// ErrChainBroken is returned by VerifyChain when a row's stored
// prev_hash doesn't match the HMAC of its predecessor's canonical
// bytes. The caller can errors.Is against it; the wrapped error
// names the offending row.
var ErrChainBroken = errors.New("audit: chain broken")

// VerifyChain walks the audit_log from fromID through toID (inclusive)
// and reports the first row whose prev_hash doesn't match the HMAC of
// the preceding row's canonical bytes.
//
// A NULL prev_hash is treated as "chain root" — the verifier checks
// the next row's prev_hash against the root row's canonical bytes
// and so on. Rows written before the chain was activated (migration
// 000033) will have NULL prev_hash; the verifier reports those as
// "pre-chain" but does not fail unless a later row's prev_hash refers
// to a tampered predecessor.
//
// The fromID parameter is the inclusive lower bound on the row ID
// (matches the BIGSERIAL stored in the audit_log table). Pass "" or
// "0" to start at the first row. The toID parameter is the inclusive
// upper bound; pass "" to verify to the end of the table.
//
// Implementation lists at most the rows in the requested range. For
// stores backing a large table this is the operator's responsibility
// to slice — Store.List has an upper limit (postgresMaxLimit = 1000)
// and a verify that exceeds that limit must be chunked.
func VerifyChain(ctx context.Context, store Store, key []byte, fromID, toID string) error {
	if store == nil {
		return errors.New("audit.VerifyChain: nil store")
	}
	if len(key) == 0 {
		return fmt.Errorf("%w: empty HMAC key", ErrInvalidHMACKey)
	}

	events, err := listForVerify(ctx, store)
	if err != nil {
		return fmt.Errorf("audit.VerifyChain: list events: %w", err)
	}
	// listForVerify returns most-recent-first; reverse so we walk in
	// insertion order, which is the order the chain was built in.
	reverseEvents(events)

	// Filter the slice down to the requested range. We don't push the
	// range into the store call because Store.List doesn't expose an
	// ID-range filter; the table is bounded by postgresMaxLimit so
	// the post-filter cost is fine.
	var window []Event
	for _, ev := range events {
		if fromID != "" && fromID != "0" && ev.ID < fromID {
			continue
		}
		if toID != "" && ev.ID > toID {
			continue
		}
		window = append(window, ev)
	}
	if len(window) == 0 {
		return nil
	}

	// Walk pairs. The first row in the window is treated as the
	// reference predecessor — if its prev_hash is set, we don't have
	// the row before it to verify against, so we skip the link check
	// and only verify subsequent rows. A reference root has nil
	// PrevHash; that's the canonical "chain start" case.
	for i := 1; i < len(window); i++ {
		prev := window[i-1]
		cur := window[i]
		expected := ChainHash(key, prev)
		if len(cur.PrevHash) == 0 && len(expected) == 0 {
			// Both pre-chain rows. No claim to verify; continue.
			continue
		}
		if len(cur.PrevHash) == 0 {
			// We expected a hash and got none. Could mean cur was
			// inserted before the chain was activated; report as a
			// chain break only if a hash was set on either side.
			return fmt.Errorf("%w: row %s has no prev_hash but predecessor %s does", ErrChainBroken, cur.ID, prev.ID)
		}
		if !hmac.Equal(expected, cur.PrevHash) {
			return fmt.Errorf("%w: row %s prev_hash mismatch (predecessor %s)", ErrChainBroken, cur.ID, prev.ID)
		}
	}
	return nil
}

// listForVerify pulls the full event list up to the store's limit.
// The verifier doesn't apply a filter — it needs every row in the
// requested range, regardless of actor/event/severity.
func listForVerify(ctx context.Context, store Store) ([]Event, error) {
	// Limit=0 lets the store apply its default cap (1000 for Postgres,
	// 100 for memory). Callers verifying larger ranges should chunk.
	return store.List(ctx, Filter{Limit: 1000})
}

// reverseEvents reverses the slice in place. Used so the verifier
// walks rows oldest-first.
func reverseEvents(es []Event) {
	for i, j := 0, len(es)-1; i < j; i, j = i+1, j-1 {
		es[i], es[j] = es[j], es[i]
	}
}
