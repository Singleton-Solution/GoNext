package log

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
)

func TestSetup_ReturnsLogger_PreStamped(t *testing.T) {
	var buf bytes.Buffer
	l, err := Setup(&buf, Options{
		Service: "api",
		Version: "v0.1.0",
		Commit:  "abc1234",
		Level:   slog.LevelInfo,
		Format:  FormatJSON,
		Redact:  true,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if l == nil {
		t.Fatal("Setup returned nil logger")
	}

	l.Info("hello")
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	for k, want := range map[string]string{
		"service": "api",
		"version": "v0.1.0",
		"commit":  "abc1234",
		"msg":     "hello",
	} {
		if got[k] != want {
			t.Errorf("field %q: got %v want %q", k, got[k], want)
		}
	}
}

func TestSetup_InstallsDefault(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	_, err := Setup(&buf, Options{
		Service: "api",
		Version: "dev",
		Commit:  "unknown",
		Level:   slog.LevelInfo,
		Format:  FormatJSON,
		Redact:  true,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	slog.Info("from default")
	if buf.Len() == 0 {
		t.Errorf("slog.Info via default did not produce output through Setup'd handler")
	}
}

func TestSetup_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		opts Options
	}{
		{"missing service", Options{Format: FormatJSON, Level: slog.LevelInfo}},
		{"bad format", Options{Service: "api", Format: "yaml", Level: slog.LevelInfo}},
		{"bad level", Options{Service: "api", Format: FormatJSON, Level: slog.Level(99)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Setup(&bytes.Buffer{}, c.opts)
			if err == nil {
				t.Errorf("expected validation error, got nil")
			}
		})
	}
}

func TestSetup_RedactDisabled_LeaksValues(t *testing.T) {
	// Sanity check: when Redact=false, sensitive keys flow through unchanged.
	// Tests sometimes want raw output. Production code must NOT use this path.
	var buf bytes.Buffer
	l, err := Setup(&buf, Options{
		Service: "test",
		Version: "v0",
		Commit:  "0",
		Level:   slog.LevelInfo,
		Format:  FormatJSON,
		Redact:  false,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	l.Info("test", "password", "hunter2")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["password"] != "hunter2" {
		t.Errorf("with Redact=false, password should be raw, got %v", got["password"])
	}
}

func TestSetup_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	_, err := Setup(&buf, Options{
		Service: "test",
		Version: "v0",
		Commit:  "0",
		Level:   slog.LevelInfo,
		Format:  FormatText,
		Redact:  true,
	})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	slog.Info("hello", "foo", "bar")
	out := buf.String()
	if !contains(out, "msg=hello") || !contains(out, "foo=bar") {
		t.Errorf("text output missing expected fields: %q", out)
	}
}

func TestOptionsFromEnv_Defaults(t *testing.T) {
	// Save and restore env between subtests.
	clearLogEnv(t)
	o := OptionsFromEnv("api")
	if o.Service != "api" {
		t.Errorf("Service: got %q want %q", o.Service, "api")
	}
	if o.Level != slog.LevelInfo {
		t.Errorf("Level default: got %v want INFO", o.Level)
	}
	if o.Format != FormatJSON {
		t.Errorf("Format default: got %q want json", o.Format)
	}
	if !o.Redact {
		t.Error("Redact should default to true")
	}
}

func TestOptionsFromEnv_Honored(t *testing.T) {
	clearLogEnv(t)
	t.Setenv("GONEXT_LOG_LEVEL", "DEBUG")
	t.Setenv("GONEXT_LOG_FORMAT", "text")
	t.Setenv("GONEXT_LOG_ADDSRC", "1")

	o := OptionsFromEnv("api")
	if o.Level != slog.LevelDebug {
		t.Errorf("Level: got %v want DEBUG", o.Level)
	}
	if o.Format != FormatText {
		t.Errorf("Format: got %q want text", o.Format)
	}
	if !o.AddSource {
		t.Errorf("AddSource: want true")
	}
}

func TestOptionsFromEnv_UnknownValuesIgnored(t *testing.T) {
	clearLogEnv(t)
	t.Setenv("GONEXT_LOG_LEVEL", "VERBOSE")
	t.Setenv("GONEXT_LOG_FORMAT", "binary")

	o := OptionsFromEnv("api")
	if o.Level != slog.LevelInfo {
		t.Errorf("unknown level should fall back to INFO, got %v", o.Level)
	}
	if o.Format != FormatJSON {
		t.Errorf("unknown format should fall back to json, got %q", o.Format)
	}
}

// ---------------------------------------------------------------------------
// helpers

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }

func clearLogEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GONEXT_LOG_LEVEL", "GONEXT_LOG_FORMAT", "GONEXT_LOG_ADDSRC"} {
		t.Setenv(k, "")
	}
	// We never expect tests to swallow errors silently, but log_test imports
	// errors purely for this assertion-of-presence (Setup signature returns
	// error).
	_ = errors.New
}
