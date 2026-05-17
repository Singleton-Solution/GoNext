package delivery

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestClassifyHTTPStatus(t *testing.T) {
	cases := []struct {
		code       int
		wantStatus Status
		wantReason string
	}{
		{200, StatusSuccess, ""},
		{201, StatusSuccess, ""},
		{204, StatusSuccess, ""},
		{299, StatusSuccess, ""},
		{400, StatusDeadletter, ReasonPermanent4xx},
		{401, StatusDeadletter, ReasonPermanent4xx},
		{403, StatusDeadletter, ReasonPermanent4xx},
		{404, StatusDeadletter, ReasonPermanent4xx},
		{422, StatusDeadletter, ReasonPermanent4xx},
		{408, StatusRetry, ""},
		{429, StatusRetry, ""},
		{410, StatusDeadletter, ReasonURLGone},
		{500, StatusRetry, ""},
		{502, StatusRetry, ""},
		{503, StatusRetry, ""},
		{504, StatusRetry, ""},
	}
	for _, tc := range cases {
		gotStatus, gotReason := classifyHTTPStatus(tc.code)
		if gotStatus != tc.wantStatus {
			t.Errorf("status %d: got %v, want %v", tc.code, gotStatus, tc.wantStatus)
		}
		if gotReason != tc.wantReason {
			t.Errorf("status %d: reason %q, want %q", tc.code, gotReason, tc.wantReason)
		}
	}
}

func TestParseRetryAfter_Seconds(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	d := parseRetryAfter("30", now)
	if d != 30*time.Second {
		t.Fatalf("expected 30s, got %v", d)
	}
}

func TestParseRetryAfter_HTTPDate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	target := now.Add(45 * time.Second)
	d := parseRetryAfter(target.Format(time.RFC1123), now)
	if d <= 0 || d > 60*time.Second {
		t.Fatalf("expected ~45s, got %v", d)
	}
}

func TestParseRetryAfter_Unparseable(t *testing.T) {
	if d := parseRetryAfter("not-a-date", time.Now()); d != 0 {
		t.Fatalf("unparseable should return 0, got %v", d)
	}
	if d := parseRetryAfter("", time.Now()); d != 0 {
		t.Fatalf("empty should return 0, got %v", d)
	}
}

func TestParseRetryAfter_CapsAt24Hours(t *testing.T) {
	d := parseRetryAfter("9999999", time.Now())
	if d != 24*time.Hour {
		t.Fatalf("expected cap at 24h, got %v", d)
	}
}

func TestDeadletterPipeline_NilPipelineIsNoop(t *testing.T) {
	var d *deadletterPipeline
	if err := d.trigger(context.Background(), Subscription{}, Payload{}, Result{}, "x"); err != nil {
		t.Fatalf("nil pipeline should return nil, got %v", err)
	}
}

func TestDeadletterPipeline_CallsBothSides(t *testing.T) {
	var auditCalls, subCalls int
	var capturedEvt DeadletterEvent
	var capturedReason string

	p := &deadletterPipeline{
		audit: AuditRecorderFunc(func(_ context.Context, e DeadletterEvent) error {
			auditCalls++
			capturedEvt = e
			return nil
		}),
		subscriptions: SubscriptionsFunc(func(_ context.Context, id, reason string) error {
			subCalls++
			capturedReason = reason
			_ = id
			return nil
		}),
	}
	sub := Subscription{ID: "sub_1", URL: "https://example.test/webhook"}
	payload := Payload{EventID: "evt_1", EventType: "post.published"}
	last := Result{Attempt: 7, HTTPStatus: 500, Err: errors.New("boom")}

	if err := p.trigger(context.Background(), sub, payload, last, ReasonScheduleExhausted); err != nil {
		t.Fatalf("trigger returned err: %v", err)
	}
	if auditCalls != 1 || subCalls != 1 {
		t.Fatalf("expected one call each, got audit=%d sub=%d", auditCalls, subCalls)
	}
	if capturedEvt.SubscriptionID != "sub_1" ||
		capturedEvt.EventID != "evt_1" ||
		capturedEvt.Reason != ReasonScheduleExhausted ||
		capturedEvt.LastStatus != 500 ||
		capturedEvt.LastError != "boom" {
		t.Fatalf("captured event mismatch: %+v", capturedEvt)
	}
	if capturedReason != ReasonScheduleExhausted {
		t.Fatalf("MarkDegraded reason = %q, want %q", capturedReason, ReasonScheduleExhausted)
	}
}

func TestDeadletterPipeline_BothSidesErrJoined(t *testing.T) {
	errA := errors.New("audit fail")
	errB := errors.New("sub fail")
	p := &deadletterPipeline{
		audit:         AuditRecorderFunc(func(context.Context, DeadletterEvent) error { return errA }),
		subscriptions: SubscriptionsFunc(func(context.Context, string, string) error { return errB }),
	}
	err := p.trigger(context.Background(), Subscription{}, Payload{}, Result{}, "x")
	if err == nil {
		t.Fatal("expected joined error")
	}
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Fatalf("expected both errs in joined: %v", err)
	}
}
