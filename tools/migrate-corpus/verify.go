package main

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// VerifyReport summarises a verify run.
type VerifyReport struct {
	Sites     int
	Summaries []VerifySummary
}

// VerifySummary is one row per verified site directory.
type VerifySummary struct {
	Slug       string
	WXRItems   int
	SQLInserts int
	ManifestOK bool
}

// Verify walks dir, re-parses every site's wxr.xml/wp_db.sql/manifest.json,
// and returns an error if any artefact is malformed. This is the small
// sanity check the issue calls out — not a full importer compatibility run.
func Verify(dir string) (*VerifyReport, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read corpus dir: %w", err)
	}
	siteDirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if !strings.HasPrefix(e.Name(), "site-") {
			continue
		}
		siteDirs = append(siteDirs, e.Name())
	}
	sort.Strings(siteDirs)
	if len(siteDirs) == 0 {
		return nil, fmt.Errorf("no site-* directories found in %s", dir)
	}

	rep := &VerifyReport{}
	for _, name := range siteDirs {
		path := filepath.Join(dir, name)
		items, err := verifyWXR(filepath.Join(path, "wxr.xml"))
		if err != nil {
			return nil, fmt.Errorf("%s: wxr: %w", name, err)
		}
		inserts, err := verifySQL(filepath.Join(path, "wp_db.sql"))
		if err != nil {
			return nil, fmt.Errorf("%s: sql: %w", name, err)
		}
		ok, err := verifyManifest(filepath.Join(path, "manifest.json"))
		if err != nil {
			return nil, fmt.Errorf("%s: manifest: %w", name, err)
		}
		rep.Summaries = append(rep.Summaries, VerifySummary{
			Slug: name, WXRItems: items, SQLInserts: inserts, ManifestOK: ok,
		})
	}
	rep.Sites = len(siteDirs)
	return rep, nil
}

// verifyWXR streams the file, asserts well-formed XML, asserts presence of
// the WP namespace, and counts <item> elements.
func verifyWXR(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	dec := xml.NewDecoder(f)
	items := 0
	sawWPNamespace := false
	sawChannel := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("xml decode: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "channel" {
				sawChannel = true
			}
			if t.Name.Local == "item" {
				items++
			}
			if t.Name.Space == "http://wordpress.org/export/1.2/" {
				sawWPNamespace = true
			}
		}
	}
	if !sawChannel {
		return 0, fmt.Errorf("no <channel> element")
	}
	if !sawWPNamespace {
		return 0, fmt.Errorf("WP export namespace (http://wordpress.org/export/1.2/) not seen")
	}
	if items == 0 {
		return 0, fmt.Errorf("no <item> elements found")
	}
	return items, nil
}

// verifySQL just confirms the file is non-empty, contains the expected
// CREATE TABLE statements, and counts INSERT lines. It is not a real SQL
// parser — that lives in the importer's `dbdirect` test layer.
func verifySQL(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := string(data)
	required := []string{
		"CREATE TABLE `wp_posts`",
		"CREATE TABLE `wp_terms`",
		"CREATE TABLE `wp_options`",
	}
	for _, r := range required {
		if !strings.Contains(s, r) {
			return 0, fmt.Errorf("missing required statement: %s", r)
		}
	}
	inserts := strings.Count(s, "INSERT INTO ")
	if inserts == 0 {
		return 0, fmt.Errorf("no INSERT statements")
	}
	return inserts, nil
}

// verifyManifest re-parses the JSON and asserts the required keys.
func verifyManifest(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return false, fmt.Errorf("json: %w", err)
	}
	required := []string{
		"profile", "site_index", "base_url", "title",
		"wxr_version", "wp_version", "counts",
	}
	for _, k := range required {
		if _, ok := m[k]; !ok {
			return false, fmt.Errorf("missing key %q", k)
		}
	}
	counts, ok := m["counts"].(map[string]any)
	if !ok {
		return false, fmt.Errorf("counts is not an object")
	}
	for _, k := range []string{"posts", "terms", "comments", "media"} {
		if _, ok := counts[k]; !ok {
			return false, fmt.Errorf("missing counts.%s", k)
		}
	}
	return true, nil
}
