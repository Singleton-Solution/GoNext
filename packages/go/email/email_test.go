package email

import (
	"errors"
	"testing"
)

func TestMessage_Validate(t *testing.T) {
	cases := []struct {
		name    string
		msg     Message
		wantErr error
	}{
		{
			name: "valid text-only",
			msg: Message{
				To:       "alice@example.com",
				Subject:  "hello",
				TextBody: "world",
			},
		},
		{
			name: "valid html-only",
			msg: Message{
				To:       "alice@example.com",
				Subject:  "hello",
				HTMLBody: "<p>world</p>",
			},
		},
		{
			name: "valid both bodies",
			msg: Message{
				To:       "alice@example.com",
				Subject:  "hello",
				TextBody: "world",
				HTMLBody: "<p>world</p>",
			},
		},
		{
			name:    "missing recipient",
			msg:     Message{Subject: "hi", TextBody: "y"},
			wantErr: ErrMissingRecipient,
		},
		{
			name:    "missing subject",
			msg:     Message{To: "a@b.test", TextBody: "y"},
			wantErr: ErrMissingSubject,
		},
		{
			name:    "missing body",
			msg:     Message{To: "a@b.test", Subject: "hi"},
			wantErr: ErrMissingBody,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.msg.Validate()
			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("Validate err: got %v want %v", err, c.wantErr)
			}
		})
	}
}
