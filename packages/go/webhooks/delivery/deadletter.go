package delivery

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Reason values used in DeadletterEvent.Reason and in the
// Subscriptions.MarkDegraded call. Kept short and machine-readable so
// the admin UI can switch on them without parsing the human-readable
// audit message.
const (
	ReasonScheduleExhausted = "schedule_exhausted"
	ReasonPermanent4xx      = "permanent_4xx"
	ReasonURLGone           = "url_gone"
	ReasonSchemeRejected    = "scheme_rejected"
	ReasonRedirectRejected  = "redirect_rejected"
)

// deadletterPipeline encapsulates the side effects of sending a delivery
// to the archive: emit an audit row, mark the subscription degraded.
// Either side can be nil — production wiring sets both; tests typically
// set only one to assert it's called.
//
// The pipeline runs best-effort: if the audit recorder or the
// subscription store fails, the error is wrapped and returned to the
// caller, who decides whether to fail the task (Asynq archives it
// either way, but the caller may want to surface the failure to logs).
type deadletterPipeline struct {
	audit         AuditRecorder
	subscriptions Subscriptions
	now           func() time.Time
}

// trigger runs the deadletter pipeline. The supplied Result is the last
// attempt's outcome; the subscription metadata identifies who to mark
// degraded; reason is one of the Reason* constants.
//
// Returns a (possibly joined) error from the audit + subscriptions
// calls. A nil return means both side effects succeeded.
func (d *deadletterPipeline) trigger(ctx context.Context, sub Subscription, p Payload, last Result, reason string) error {
	if d == nil {
		return nil
	}
	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	var errs []error
	if d.audit != nil {
		errMsg := ""
		if last.Err != nil {
			errMsg = last.Err.Error()
		}
		evt := DeadletterEvent{
			SubscriptionID: sub.ID,
			EventID:        p.EventID,
			EventType:      p.EventType,
			URL:            sub.URL,
			Attempts:       last.Attempt,
			LastStatus:     last.HTTPStatus,
			LastError:      errMsg,
			Reason:         reason,
			OccurredAt:     now,
		}
		if err := d.audit.RecordDeadletter(ctx, evt); err != nil {
			errs = append(errs, err)
		}
	}
	if d.subscriptions != nil {
		if err := d.subscriptions.MarkDegraded(ctx, sub.ID, reason); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// classifyHTTPStatus returns the Status and Reason that should apply to
// a completed HTTP attempt with the given response code.
//
// 2xx → StatusSuccess. 408 and 429 → StatusRetry (transient by RFC
// 7231 §6.5.7 / 6.5.18). 410 → StatusDeadletter, reason ReasonURLGone
// (the subscriber has explicitly told us the resource is gone — doc 12
// §14.4 calls this out). 4xx other than 408/429/410 → StatusDeadletter
// reason ReasonPermanent4xx. 5xx → StatusRetry.
//
// 3xx is unreachable here because our http.Client rejects redirects in
// CheckRedirect before they become a "response" we'd classify.
func classifyHTTPStatus(code int) (Status, string) {
	switch {
	case code >= 200 && code < 300:
		return StatusSuccess, ""
	case code == 408 || code == 429:
		return StatusRetry, ""
	case code == 410:
		return StatusDeadletter, ReasonURLGone
	case code >= 400 && code < 500:
		return StatusDeadletter, ReasonPermanent4xx
	case code >= 500 && code < 600:
		return StatusRetry, ""
	default:
		// Anything weird (1xx synthesized, unexpected 6xx from a buggy
		// proxy) is treated as transient — retry rather than archive,
		// because we can't be sure what we're looking at.
		return StatusRetry, ""
	}
}

// parseRetryAfter parses the Retry-After header per RFC 7231 §7.1.3.
//
// The header can be either an integer number of seconds or an
// HTTP-date. We accept both. Unparseable values return 0 — the caller
// then falls back to its scheduler.
//
// The return is capped against a sane maximum (24h) so a pathological
// subscriber can't pin a single delivery into the future indefinitely.
func parseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	value = strings.TrimSpace(value)
	if n, err := strconv.Atoi(value); err == nil && n >= 0 {
		d := time.Duration(n) * time.Second
		return capRetryAfter(d)
	}
	// Try a few date formats. RFC 7231 says HTTP-date in RFC1123 or
	// related; time.RFC1123 is the canonical one. We also tolerate the
	// older asctime() form for paranoid completeness.
	for _, layout := range []string{time.RFC1123, time.RFC1123Z, time.RFC850, time.ANSIC} {
		if t, err := time.Parse(layout, value); err == nil {
			if t.After(now) {
				return capRetryAfter(t.Sub(now))
			}
			return 0
		}
	}
	return 0
}

func capRetryAfter(d time.Duration) time.Duration {
	const max = 24 * time.Hour
	if d > max {
		return max
	}
	if d < 0 {
		return 0
	}
	return d
}
