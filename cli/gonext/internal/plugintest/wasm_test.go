package plugintest

import (
	"bytes"
	"strings"
	"testing"
)

// validHeader is the smallest legal WebAssembly v1 binary: 4-byte magic +
// little-endian u32 version 1. No sections. The header check should accept
// it; full bytecode validation is the runtime's job.
var validHeader = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

func TestValidateWASMHeader(t *testing.T) {
	cases := []struct {
		name    string
		in      []byte
		wantSub string // substring expected in error; "" = no error
	}{
		{name: "valid", in: validHeader, wantSub: ""},
		{name: "empty", in: nil, wantSub: "empty"},
		{name: "too short", in: []byte{0x00, 0x61, 0x73}, wantSub: "too short"},
		{
			name:    "bad magic",
			in:      []byte{0xde, 0xad, 0xbe, 0xef, 0x01, 0x00, 0x00, 0x00},
			wantSub: "bad magic",
		},
		{
			name:    "wrong version",
			in:      []byte{0x00, 0x61, 0x73, 0x6d, 0x02, 0x00, 0x00, 0x00},
			wantSub: "version",
		},
		{
			name:    "exceeds size cap",
			in:      append(bytes.Clone(validHeader), make([]byte, maxWASMSize+1)...),
			wantSub: "exceeds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateWASMHeader(tc.in)
			if tc.wantSub == "" {
				if err != nil {
					t.Fatalf("expected no error; got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q; got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected error containing %q; got %q", tc.wantSub, err.Error())
			}
		})
	}
}
