package shortcode

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Singleton-Solution/GoNext/packages/go/migrate/html2blocks"
)

func TestParseMode(t *testing.T) {
	cases := []struct {
		in   string
		want Mode
		err  bool
	}{
		{"", ModeMap, false},
		{"map", ModeMap, false},
		{"Preserve", ModePreserve, false},
		{"STRIP", ModeStrip, false},
		{"bogus", ModeMap, true},
	}
	for _, tc := range cases {
		got, err := ParseMode(tc.in)
		if (err != nil) != tc.err {
			t.Errorf("ParseMode(%q) err=%v want err=%v", tc.in, err, tc.err)
		}
		if got != tc.want {
			t.Errorf("ParseMode(%q) = %v want %v", tc.in, got, tc.want)
		}
	}
}

func TestMode_String(t *testing.T) {
	if ModeMap.String() != "map" || ModePreserve.String() != "preserve" || ModeStrip.String() != "strip" {
		t.Error("Mode.String mismatch")
	}
}

func TestProcess_MapMode_BuiltinCaption(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterDefaults()

	src := `[caption id="att_5" align="aligncenter" width="300"]<img src="https://example.com/a.jpg" />A photo[/caption]`
	res, err := ProcessString(src, Options{Mode: ModeMap, Registry: reg})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if res.ProcessedCount != 1 || res.MappedCount != 1 {
		t.Errorf("counters: %+v", res)
	}
	if len(res.Blocks) != 1 {
		t.Fatalf("blocks: got %d want 1", len(res.Blocks))
	}
	b := res.Blocks[0]
	if b.Name != html2blocks.BlockImage {
		t.Errorf("block name: %q", b.Name)
	}
	if b.Attrs["caption"] != "A photo" {
		t.Errorf("caption: %q", b.Attrs["caption"])
	}
	if b.Attrs["align"] != "aligncenter" {
		t.Errorf("align: %q", b.Attrs["align"])
	}
}

func TestProcess_MapMode_Gallery(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterDefaults()
	src := `[gallery ids="1,2 ,3" columns="3" link="file"]`
	res, _ := ProcessString(src, Options{Mode: ModeMap, Registry: reg})
	if len(res.Blocks) != 1 {
		t.Fatalf("blocks: got %d", len(res.Blocks))
	}
	ids, _ := res.Blocks[0].Attrs["ids"].([]string)
	want := []string{"1", "2", "3"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("ids: got %v want %v", ids, want)
	}
	if res.Blocks[0].Attrs["columns"] != "3" {
		t.Errorf("columns: %v", res.Blocks[0].Attrs["columns"])
	}
}

func TestProcess_MapMode_Video(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterDefaults()
	res, _ := ProcessString(`[video mp4="x.mp4" autoplay="1" /]`, Options{Mode: ModeMap, Registry: reg})
	if len(res.Blocks) != 1 || res.Blocks[0].Name != "core/video" {
		t.Fatalf("blocks: %v", res.Blocks)
	}
	if res.Blocks[0].Attrs["src"] != "x.mp4" {
		t.Errorf("src: %v", res.Blocks[0].Attrs["src"])
	}
	if res.Blocks[0].Attrs["autoplay"] != true {
		t.Errorf("autoplay: %v", res.Blocks[0].Attrs["autoplay"])
	}
}

func TestProcess_MapMode_Audio(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterDefaults()
	res, _ := ProcessString(`[audio mp3="x.mp3" /]`, Options{Mode: ModeMap, Registry: reg})
	if len(res.Blocks) != 1 || res.Blocks[0].Name != "core/audio" {
		t.Fatalf("blocks: %v", res.Blocks)
	}
}

func TestProcess_MapMode_Embed(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterDefaults()
	res, _ := ProcessString(`[embed]https://youtu.be/abc[/embed]`, Options{Mode: ModeMap, Registry: reg})
	if len(res.Blocks) != 1 || res.Blocks[0].Name != "core/embed" {
		t.Fatalf("blocks: %v", res.Blocks)
	}
	if res.Blocks[0].Attrs["url"] != "https://youtu.be/abc" {
		t.Errorf("url: %v", res.Blocks[0].Attrs["url"])
	}
}

func TestProcess_MapMode_ContactForm7(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterDefaults()
	res, _ := ProcessString(`[contact-form-7 id="42" title="Contact"]`, Options{Mode: ModeMap, Registry: reg})
	if len(res.Blocks) != 1 || res.Blocks[0].Name != "core/shortcode" {
		t.Fatalf("blocks: %v", res.Blocks)
	}
	if res.Blocks[0].Attrs["formId"] != "42" {
		t.Errorf("formId: %v", res.Blocks[0].Attrs["formId"])
	}
}

func TestProcess_MapMode_UnregisteredFallsBack(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterDefaults()
	src := `before [bogus foo="bar"]inner[/bogus] after`
	res, _ := ProcessString(src, Options{Mode: ModeMap, Registry: reg})
	if res.MappedCount != 0 {
		t.Errorf("MappedCount: got %d want 0", res.MappedCount)
	}
	if res.FellBackCount != 1 {
		t.Errorf("FellBackCount: got %d want 1", res.FellBackCount)
	}
	// Look for the core/html block that carries the raw shortcode.
	var found bool
	for _, b := range res.Blocks {
		if b.Name == "core/html" {
			if c, _ := b.Attrs["content"].(string); strings.Contains(c, "[bogus foo=\"bar\"]") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("did not find preserved core/html block: %#v", res.Blocks)
	}
}

func TestProcess_PreserveMode(t *testing.T) {
	src := `[caption]some caption[/caption]`
	res, _ := ProcessString(src, Options{Mode: ModePreserve})
	if res.FellBackCount != 1 {
		t.Errorf("FellBackCount: %d", res.FellBackCount)
	}
	if len(res.Blocks) != 1 {
		t.Fatalf("blocks: %d", len(res.Blocks))
	}
	if res.Blocks[0].Name != "core/html" {
		t.Errorf("block name: %q", res.Blocks[0].Name)
	}
	if res.Blocks[0].Attrs["content"] != src {
		t.Errorf("content: %q", res.Blocks[0].Attrs["content"])
	}
}

func TestProcess_StripMode(t *testing.T) {
	src := `before [bigtag]inner text[/bigtag] after [selfclose /]`
	res, _ := ProcessString(src, Options{Mode: ModeStrip})
	if res.StrippedCount != 2 {
		t.Errorf("StrippedCount: %d want 2", res.StrippedCount)
	}
	// Self-closing dropped, enclosing inner preserved as plain text.
	var stripped string
	for _, b := range res.Blocks {
		c, _ := b.Attrs["content"].(string)
		stripped += c
	}
	if !strings.Contains(stripped, "inner text") {
		t.Errorf("inner text missing: %q", stripped)
	}
	if strings.Contains(stripped, "[bigtag]") || strings.Contains(stripped, "[selfclose") {
		t.Errorf("shortcode markers leaked: %q", stripped)
	}
}

func TestProcess_OverrideTranslator(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterDefaults()
	// Override caption to emit a paragraph.
	reg.Register("caption", func(sc Shortcode) []html2blocks.Block {
		return []html2blocks.Block{{
			Name:  html2blocks.BlockParagraph,
			Attrs: map[string]any{"content": sc.Inner},
		}}
	})
	res, _ := ProcessString(`[caption]hi[/caption]`, Options{Mode: ModeMap, Registry: reg})
	if len(res.Blocks) != 1 || res.Blocks[0].Name != html2blocks.BlockParagraph {
		t.Errorf("override not honoured: %v", res.Blocks)
	}
}

func TestProcess_LiteralBetweenShortcodes(t *testing.T) {
	reg := NewRegistry()
	reg.RegisterDefaults()
	src := `hello [video src="a.mp4" /] world [audio mp3="b.mp3" /] end`
	res, _ := ProcessString(src, Options{Mode: ModeMap, Registry: reg})
	if res.ProcessedCount != 2 {
		t.Errorf("ProcessedCount: %d", res.ProcessedCount)
	}
	// Expect 5 blocks: paragraph("hello "), video, paragraph(" world "), audio, paragraph(" end")
	if len(res.Blocks) != 5 {
		t.Fatalf("blocks: got %d want 5 (%+v)", len(res.Blocks), res.Blocks)
	}
}

func TestProcess_EmptyInput(t *testing.T) {
	res, err := ProcessString("", Options{Mode: ModeMap})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(res.Blocks) != 0 || res.ProcessedCount != 0 {
		t.Errorf("empty input: %+v", res)
	}
}
