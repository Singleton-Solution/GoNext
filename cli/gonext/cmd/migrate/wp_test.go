package migrate

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunWP_MissingFile rejects the call before any DB access
// when --file is omitted.
func TestRunWP_MissingFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"wp"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "--file is required") {
		t.Errorf("expected --file is required, got %q", stderr.String())
	}
}

// TestRunWP_UnknownConflictPolicy surfaces a usage error rather
// than silently defaulting.
func TestRunWP_UnknownConflictPolicy(t *testing.T) {
	tmp := writeTempWXR(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"wp", "--file", tmp, "--on-conflict", "bogus"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d (stderr=%q)", code, ExitUsage, stderr.String())
	}
}

// TestRunWP_Dryrun_NoDSN walks the WXR without DATABASE_URL set
// and prints a summary. Confirms the dry-run path bypasses the
// DB completely.
func TestRunWP_Dryrun_NoDSN(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	tmp := writeTempWXR(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"wp", "--file", tmp, "--dry-run"}, &stdout, &stderr)
	if code != ExitOK {
		t.Errorf("exit: got %d, want %d (stderr=%q)", code, ExitOK, stderr.String())
	}
	if !strings.Contains(stdout.String(), "[dry-run] import summary") {
		t.Errorf("expected dry-run summary on stdout, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "posts:       1") {
		t.Errorf("expected posts: 1 line, got %q", stdout.String())
	}
}

// TestRunWP_NoDSN_NonDryrun rejects the call when DATABASE_URL is
// unset and --dry-run is not passed.
func TestRunWP_NoDSN_NonDryrun(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	tmp := writeTempWXR(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"wp", "--file", tmp}, &stdout, &stderr)
	if code != ExitUsage {
		t.Errorf("exit: got %d, want %d (stderr=%q)", code, ExitUsage, stderr.String())
	}
	if !strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Errorf("expected DATABASE_URL on stderr, got %q", stderr.String())
	}
}

// TestRunWP_FileNotFound surfaces a fail-exit when the path does
// not exist.
func TestRunWP_FileNotFound(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run([]string{"wp", "--file", "/nope/does/not/exist.xml", "--dry-run"}, &stdout, &stderr)
	if code != ExitFail {
		t.Errorf("exit: got %d, want %d (stderr=%q)", code, ExitFail, stderr.String())
	}
}

// writeTempWXR drops a 1-post WXR file in t.TempDir and returns
// the path. Mirrors the importer's minimal.xml so the CLI tests
// don't need a relative path traversal back into the package
// directory.
func writeTempWXR(t *testing.T) string {
	t.Helper()
	const body = `<?xml version="1.0" encoding="UTF-8" ?>
<rss version="2.0"
  xmlns:content="http://purl.org/rss/1.0/modules/content/"
  xmlns:dc="http://purl.org/dc/elements/1.1/"
  xmlns:wp="http://wordpress.org/export/1.2/">
<channel>
  <title>CLI Test</title>
  <link>https://cli.example.com</link>
  <description>cli</description>
  <wp:wxr_version>1.2</wp:wxr_version>
  <wp:base_site_url>https://cli.example.com</wp:base_site_url>
  <wp:base_blog_url>https://cli.example.com</wp:base_blog_url>
  <generator>test</generator>
  <wp:author>
    <wp:author_id>1</wp:author_id>
    <wp:author_login><![CDATA[cli]]></wp:author_login>
    <wp:author_email><![CDATA[cli@cli.example.com]]></wp:author_email>
    <wp:author_display_name><![CDATA[CLI]]></wp:author_display_name>
  </wp:author>
  <item>
    <title>Post</title>
    <dc:creator><![CDATA[cli]]></dc:creator>
    <content:encoded><![CDATA[<p>Hello.</p>]]></content:encoded>
    <wp:post_id>1</wp:post_id>
    <wp:post_date><![CDATA[2024-01-01 00:00:00]]></wp:post_date>
    <wp:post_date_gmt><![CDATA[2024-01-01 00:00:00]]></wp:post_date_gmt>
    <wp:post_name><![CDATA[post]]></wp:post_name>
    <wp:status>publish</wp:status>
    <wp:post_type>post</wp:post_type>
  </item>
</channel>
</rss>
`
	path := filepath.Join(t.TempDir(), "in.xml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}
