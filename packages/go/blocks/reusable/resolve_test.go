package reusable

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// mustEncode is a tiny test-only helper that JSON-encodes v and
// returns the bytes, t.Fatal'ing on error.
func mustEncode(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

func TestResolveRefs_SimpleSubstitution(t *testing.T) {
	s := NewMemoryStore()
	inner := []BlockNode{
		{Type: "core/paragraph", Attributes: mustEncode(t, map[string]string{"text": "hi"})},
	}
	entry, err := s.Create(context.Background(), Entry{
		Name:    "snippet",
		Content: mustEncode(t, inner),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	input := []BlockNode{
		{
			Type:       RefBlockType,
			Attributes: mustEncode(t, map[string]string{"ref": entry.ID.String()}),
		},
		{Type: "core/heading", Attributes: mustEncode(t, map[string]string{"text": "after"})},
	}
	resolved, err := ResolveRefs(context.Background(), s, input)
	if err != nil {
		t.Fatalf("ResolveRefs: %v", err)
	}
	if len(resolved) != 2 {
		t.Fatalf("resolved len = %d, want 2", len(resolved))
	}
	if resolved[0].Type != "core/paragraph" {
		t.Fatalf("first node type = %q, want core/paragraph", resolved[0].Type)
	}
	if resolved[1].Type != "core/heading" {
		t.Fatalf("trailing node lost: %+v", resolved[1])
	}
}

func TestResolveRefs_MissingEntry(t *testing.T) {
	s := NewMemoryStore()
	input := []BlockNode{
		{
			Type:       RefBlockType,
			Attributes: mustEncode(t, map[string]string{"ref": uuid.New().String()}),
		},
	}
	resolved, err := ResolveRefs(context.Background(), s, input)
	if err != nil {
		t.Fatalf("ResolveRefs: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Type != MissingBlockType {
		t.Fatalf("expected missing sentinel, got %+v", resolved)
	}
}

func TestResolveRefs_InvalidRefAttrs(t *testing.T) {
	s := NewMemoryStore()
	// Ref attr that's not a UUID.
	input := []BlockNode{
		{Type: RefBlockType, Attributes: mustEncode(t, map[string]string{"ref": "not-a-uuid"})},
	}
	resolved, err := ResolveRefs(context.Background(), s, input)
	if err != nil {
		t.Fatalf("ResolveRefs: %v", err)
	}
	if resolved[0].Type != MissingBlockType {
		t.Fatalf("expected missing sentinel for bad ref, got %+v", resolved)
	}
}

func TestResolveRefs_RecursesIntoInnerBlocks(t *testing.T) {
	s := NewMemoryStore()
	inner := []BlockNode{
		{Type: "core/paragraph", Attributes: mustEncode(t, map[string]string{"text": "inner"})},
	}
	entry, err := s.Create(context.Background(), Entry{
		Name:    "snippet",
		Content: mustEncode(t, inner),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	input := []BlockNode{
		{
			Type:       "core/columns",
			Attributes: json.RawMessage(`{}`),
			InnerBlocks: []BlockNode{
				{Type: RefBlockType, Attributes: mustEncode(t, map[string]string{"ref": entry.ID.String()})},
			},
		},
	}
	resolved, err := ResolveRefs(context.Background(), s, input)
	if err != nil {
		t.Fatalf("ResolveRefs: %v", err)
	}
	if len(resolved) != 1 || len(resolved[0].InnerBlocks) != 1 {
		t.Fatalf("structure not preserved: %+v", resolved)
	}
	if resolved[0].InnerBlocks[0].Type != "core/paragraph" {
		t.Fatalf("nested ref not resolved: %+v", resolved[0].InnerBlocks[0])
	}
}

func TestResolveRefs_CycleBreaksToMissing(t *testing.T) {
	s := NewMemoryStore()
	// Entry A references entry B; entry B references A. The cycle
	// must terminate, producing a missing sentinel on the second
	// visit.
	idA := uuid.New()
	idB := uuid.New()

	contentA := mustEncode(t, []BlockNode{
		{Type: RefBlockType, Attributes: mustEncode(t, map[string]string{"ref": idB.String()})},
	})
	contentB := mustEncode(t, []BlockNode{
		{Type: RefBlockType, Attributes: mustEncode(t, map[string]string{"ref": idA.String()})},
	})

	if _, err := s.Create(context.Background(), Entry{ID: idA, Name: "A", Content: contentA}); err != nil {
		t.Fatalf("Create A: %v", err)
	}
	if _, err := s.Create(context.Background(), Entry{ID: idB, Name: "B", Content: contentB}); err != nil {
		t.Fatalf("Create B: %v", err)
	}

	input := []BlockNode{
		{Type: RefBlockType, Attributes: mustEncode(t, map[string]string{"ref": idA.String()})},
	}
	resolved, err := ResolveRefs(context.Background(), s, input)
	if err != nil {
		t.Fatalf("ResolveRefs: %v", err)
	}
	// We expect: A's content (a ref to B) → B's content (a ref to A,
	// which is the cycle and renders as missing).
	if len(resolved) != 1 || resolved[0].Type != MissingBlockType {
		t.Fatalf("expected missing sentinel at cycle, got %+v", resolved)
	}
}

func TestResolveRefs_EmptyTree(t *testing.T) {
	s := NewMemoryStore()
	resolved, err := ResolveRefs(context.Background(), s, nil)
	if err != nil {
		t.Fatalf("ResolveRefs nil: %v", err)
	}
	if len(resolved) != 0 {
		t.Fatalf("expected empty, got %v", resolved)
	}
}
