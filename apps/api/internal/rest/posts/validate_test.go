package posts

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestValidateCreate_Status(t *testing.T) {
	t.Parallel()

	good := "draft"
	if err := validateCreate(CreateInput{Status: &good}); err != nil {
		t.Errorf("validateCreate(draft) = %v", err)
	}
	bad := "frobnicate"
	err := validateCreate(CreateInput{Status: &bad})
	if v, ok := asValidation(err); !ok || v.Code != "invalid_status" {
		t.Errorf("err = %v, want invalid_status", err)
	}
}

func TestValidateSlug(t *testing.T) {
	t.Parallel()

	cases := []struct {
		slug    string
		wantErr bool
	}{
		{"", false},
		{"hello", false},
		{"hello-world", false},
		{"hello_world", false},
		{"hello-WORLD-42", false},
		{"hello world", true},
		{"hello/world", true},
		{"hello!", true},
		{"héllo", true}, // non-ASCII
	}
	for _, tc := range cases {
		err := validateSlug(tc.slug)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateSlug(%q) err = %v, wantErr = %v", tc.slug, err, tc.wantErr)
		}
	}
}

func TestValidateContentBlocks(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"empty array", `[]`, false},
		{"valid", `[{"type":"core/paragraph","content":"hi"}]`, false},
		{"missing type", `[{"content":"x"}]`, true},
		{"non-string type", `[{"type":42}]`, true},
		{"not array", `{"type":"core/paragraph"}`, true},
		{"strings in array", `["x"]`, true},
		{"malformed", `[}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateContentBlocks(json.RawMessage(tc.raw))
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidationError_Implements(t *testing.T) {
	t.Parallel()

	v := validation{Code: "x", Detail: "details"}
	if v.Error() != "details" {
		t.Errorf("Error() = %q", v.Error())
	}
	var v2 validation
	if !errors.As(v, &v2) {
		t.Errorf("errors.As did not match")
	}
}

func TestValidateCreate_TitleTooLong(t *testing.T) {
	t.Parallel()

	huge := make([]byte, titleMaxBytes+1)
	for i := range huge {
		huge[i] = 'x'
	}
	s := string(huge)
	err := validateCreate(CreateInput{Title: &s})
	if v, ok := asValidation(err); !ok || v.Code != "title_too_long" {
		t.Errorf("err = %v, want title_too_long", err)
	}
}

func TestValidateCreate_CommentStatus(t *testing.T) {
	t.Parallel()

	bad := "maybe"
	err := validateCreate(CreateInput{CommentStatus: &bad})
	if v, ok := asValidation(err); !ok || v.Code != "invalid_comment_status" {
		t.Errorf("err = %v", err)
	}
	bad = "perhaps"
	err = validateCreate(CreateInput{PingStatus: &bad})
	if v, ok := asValidation(err); !ok || v.Code != "invalid_ping_status" {
		t.Errorf("err = %v", err)
	}
}

func TestValidateCreate_Meta(t *testing.T) {
	t.Parallel()

	err := validateCreate(CreateInput{Meta: json.RawMessage(`[1,2,3]`)})
	if v, ok := asValidation(err); !ok || v.Code != "invalid_meta" {
		t.Errorf("err = %v, want invalid_meta", err)
	}
}
