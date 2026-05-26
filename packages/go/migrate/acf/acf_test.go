package acf

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse_SingleObject(t *testing.T) {
	t.Parallel()
	data := mustRead(t, "testdata/blog_group.json")
	exp, err := Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := len(exp.Groups), 1; got != want {
		t.Fatalf("groups: got %d want %d", got, want)
	}
	if got, want := exp.Groups[0].Key, "group_blog_meta"; got != want {
		t.Fatalf("key: got %q want %q", got, want)
	}
}

func TestParse_Array(t *testing.T) {
	t.Parallel()
	data := mustRead(t, "testdata/bundle.json")
	exp, err := Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, want := len(exp.Groups), 2; got != want {
		t.Fatalf("groups: got %d want %d", got, want)
	}
}

func TestParse_NilReader(t *testing.T) {
	t.Parallel()
	if _, err := Parse(nil); err == nil {
		t.Fatal("want error for nil reader")
	}
}

func TestParse_Empty(t *testing.T) {
	t.Parallel()
	if _, err := Parse(bytes.NewReader(nil)); err == nil {
		t.Fatal("want error for empty input")
	}
	if _, err := Parse(strings.NewReader("not json")); err == nil {
		t.Fatal("want error for malformed input")
	}
}

func TestMapFieldGroup_BlogMeta(t *testing.T) {
	t.Parallel()
	exp := mustParse(t, "testdata/blog_group.json")
	g := exp.Groups[0]
	schema, rep, err := MapFieldGroup(&g, nil)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if got, want := schema.Title, "Blog Meta"; got != want {
		t.Fatalf("title: got %q want %q", got, want)
	}
	if got, want := schema.Key, "group_blog_meta"; got != want {
		t.Fatalf("key: got %q want %q", got, want)
	}
	if got, want := schema.PostTypes, []string{"post"}; !equalSlice(got, want) {
		t.Fatalf("post_types: got %v want %v", got, want)
	}
	if got, want := len(schema.Fields), 5; got != want {
		t.Fatalf("fields: got %d want %d", got, want)
	}
	if got, want := rep.Imported, 5; got != want {
		t.Fatalf("imported: got %d want %d", got, want)
	}
	if len(rep.Warnings) != 0 {
		t.Fatalf("warnings: want none, got %v", rep.Warnings)
	}

	// Spot-check the field shapes.
	byName := map[string]SchemaField{}
	for _, f := range schema.Fields {
		byName[f.Name] = f
	}
	if got := byName["subtitle"].Kind; got != "text" {
		t.Errorf("subtitle.kind: got %q want %q", got, "text")
	}
	if got := byName["hero_image"].Kind; got != "image" {
		t.Errorf("hero_image.kind: got %q want %q", got, "image")
	}
	if got := byName["featured"].Kind; got != "boolean" {
		t.Errorf("featured.kind: got %q want %q", got, "boolean")
	}
	sec := byName["section"]
	if sec.Kind != "select" || len(sec.Choices) != 3 {
		t.Errorf("section: kind=%q choices=%d", sec.Kind, len(sec.Choices))
	}
	// Choices should be sorted by value for determinism.
	wantValues := []string{"news", "release", "tutorials"}
	for i, c := range sec.Choices {
		if c.Value != wantValues[i] {
			t.Errorf("choice[%d].value: got %q want %q", i, c.Value, wantValues[i])
		}
	}
	if got := byName["author_ref"].Kind; got != "user_ref" {
		t.Errorf("author_ref.kind: got %q want %q", got, "user_ref")
	}
}

func TestMapFieldGroup_RepeaterAndUnknown(t *testing.T) {
	t.Parallel()
	exp := mustParse(t, "testdata/team_repeater.json")
	g := exp.Groups[0]
	schema, rep, err := MapFieldGroup(&g, nil)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	// One repeater imported, one message presentational (skipped),
	// one unknown type (skipped). Imported counts only what survives.
	if got, want := rep.Imported, 1; got != want {
		t.Fatalf("imported: got %d want %d", got, want)
	}
	if got, want := len(rep.Warnings), 2; got != want {
		t.Fatalf("warnings: got %d want %d (%v)", got, want, rep.Warnings)
	}
	if got, want := schema.Fields[0].Kind, "repeater"; got != want {
		t.Fatalf("kind: got %q want %q", got, want)
	}
	if got, want := len(schema.Fields[0].Children), 3; got != want {
		t.Fatalf("children: got %d want %d", got, want)
	}
}

func TestMapFieldGroup_NilGroup(t *testing.T) {
	t.Parallel()
	if _, _, err := MapFieldGroup(nil, nil); err == nil {
		t.Fatal("want error for nil group")
	}
}

func TestMapFieldGroup_MissingKey(t *testing.T) {
	t.Parallel()
	if _, _, err := MapFieldGroup(&FieldGroup{Title: "x"}, nil); err == nil {
		t.Fatal("want error for missing key")
	}
}

func TestMapPostValues_FlatFields(t *testing.T) {
	t.Parallel()
	exp := mustParse(t, "testdata/blog_group.json")
	g := exp.Groups[0]
	schema, _, err := MapFieldGroup(&g, nil)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	postMeta := map[string]string{
		"subtitle":   "About the team",
		"hero_image": "412",
		"featured":   "1",
		"section":    "news",
		"author_ref": "7",
	}
	values, err := MapPostValues(schema, postMeta)
	if err != nil {
		t.Fatalf("map values: %v", err)
	}
	if got, want := len(values), 5; got != want {
		t.Fatalf("values: got %d want %d", got, want)
	}
	got := map[string]string{}
	for _, v := range values {
		got[v.Key] = v.Value
	}
	if got["featured"] != "true" {
		t.Errorf("featured: want %q got %q", "true", got["featured"])
	}
	if got["hero_image"] != "412" {
		t.Errorf("hero_image: want %q got %q", "412", got["hero_image"])
	}
}

func TestMapPostValues_Repeater(t *testing.T) {
	t.Parallel()
	exp := mustParse(t, "testdata/team_repeater.json")
	g := exp.Groups[0]
	schema, _, err := MapFieldGroup(&g, nil)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	postMeta := map[string]string{
		"team":          "2",
		"team_0_name":   "Alex",
		"team_0_role":   "Lead",
		"team_0_photo":  "123",
		"team_1_name":   "Sam",
		"team_1_role":   "Designer",
		"team_1_photo":  "124",
	}
	values, err := MapPostValues(schema, postMeta)
	if err != nil {
		t.Fatalf("map values: %v", err)
	}
	if got, want := len(values), 6; got != want {
		t.Fatalf("values: got %d want %d", got, want)
	}
	// Spot-check key shape and values.
	got := map[string]string{}
	for _, v := range values {
		got[v.Key] = v.Value
	}
	if got["team.0.name"] != "Alex" {
		t.Errorf("team.0.name: want %q got %q", "Alex", got["team.0.name"])
	}
	if got["team.1.photo"] != "124" {
		t.Errorf("team.1.photo: want %q got %q", "124", got["team.1.photo"])
	}
}

func TestMapPostValues_RepeaterMalformed(t *testing.T) {
	t.Parallel()
	exp := mustParse(t, "testdata/team_repeater.json")
	g := exp.Groups[0]
	schema, _, err := MapFieldGroup(&g, nil)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	postMeta := map[string]string{"team": "not-a-number"}
	if _, err := MapPostValues(schema, postMeta); err == nil {
		t.Fatal("want error on malformed row count")
	}
}

func TestMapPostValues_NilSchema(t *testing.T) {
	t.Parallel()
	if _, err := MapPostValues(nil, nil); err == nil {
		t.Fatal("want error for nil schema")
	}
}

func TestMapPostValues_Relationship_Multi(t *testing.T) {
	t.Parallel()
	exp := mustParse(t, "testdata/bundle.json")
	// "group_related" is the second group.
	g := exp.Groups[1]
	schema, _, err := MapFieldGroup(&g, nil)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if got, want := schema.Fields[0].Kind, "post_ref"; got != want {
		t.Fatalf("kind: got %q want %q", got, want)
	}
	if !schema.Fields[0].Multiple {
		t.Fatal("relationship must map to multiple")
	}
	postMeta := map[string]string{"related_posts": " 1, 2, 3 "}
	values, err := MapPostValues(schema, postMeta)
	if err != nil {
		t.Fatalf("map values: %v", err)
	}
	if values[0].Value != "1,2,3" {
		t.Errorf("normalised list: got %q want %q", values[0].Value, "1,2,3")
	}
}

func TestSchema_AsJSON(t *testing.T) {
	t.Parallel()
	exp := mustParse(t, "testdata/blog_group.json")
	g := exp.Groups[0]
	schema, _, err := MapFieldGroup(&g, nil)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	b, err := schema.AsJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"key": "group_blog_meta"`) {
		t.Errorf("output missing key, got %q", string(b))
	}
}

// helpers

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}

func mustParse(t *testing.T, p string) *FieldGroupExport {
	t.Helper()
	exp, err := Parse(bytes.NewReader(mustRead(t, p)))
	if err != nil {
		t.Fatalf("parse %s: %v", p, err)
	}
	return exp
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
