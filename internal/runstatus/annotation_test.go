package runstatus_test

import (
	"path/filepath"
	"testing"
	"time"

	"kitsoki/internal/runstatus"
)

// TestAnnotationPath verifies the expected path format.
func TestAnnotationPath(t *testing.T) {
	dir := "/tmp/sessions/bugfix"
	sid := "abc123"
	got := runstatus.AnnotationPath(dir, sid)
	want := filepath.Join(dir, sid+".annotations.jsonl")
	if got != want {
		t.Errorf("AnnotationPath(%q, %q) = %q; want %q", dir, sid, got, want)
	}
}

// TestLoadAnnotations_empty verifies that a non-existent file returns an empty
// slice and no error.
func TestLoadAnnotations_empty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-session.annotations.jsonl")
	anns, err := runstatus.LoadAnnotations(path)
	if err != nil {
		t.Fatalf("LoadAnnotations on non-existent file: unexpected error: %v", err)
	}
	if anns == nil {
		t.Fatal("LoadAnnotations on non-existent file: want empty slice, got nil")
	}
	if len(anns) != 0 {
		t.Fatalf("LoadAnnotations on non-existent file: want 0 annotations, got %d", len(anns))
	}
}

// TestAppendAnnotation_roundtrip appends two annotations, loads them back, and
// asserts that the fields survive a JSONL round-trip.
func TestAppendAnnotation_roundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sess.annotations.jsonl")

	score1 := 0.9
	a1 := runstatus.Annotation{
		Ts:           time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		SessionID:    "sess-001",
		TargetCallID: "deadbeef12345678",
		Score:        &score1,
		Label:        "good",
		Comment:      "agent picked the right intent",
		Annotator:    "alice@example.com",
	}
	a2 := runstatus.Annotation{
		Ts:         time.Date(2026, 1, 2, 3, 5, 0, 0, time.UTC),
		SessionID:  "sess-001",
		TargetTurn: 3,
		Label:      "off-topic",
		Comment:    "turn 3 diverged",
		Annotator:  "bob@example.com",
	}

	if err := runstatus.AppendAnnotation(path, a1); err != nil {
		t.Fatalf("AppendAnnotation a1: %v", err)
	}
	if err := runstatus.AppendAnnotation(path, a2); err != nil {
		t.Fatalf("AppendAnnotation a2: %v", err)
	}

	got, err := runstatus.LoadAnnotations(path)
	if err != nil {
		t.Fatalf("LoadAnnotations: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 annotations, got %d", len(got))
	}

	// Check first annotation.
	g1 := got[0]
	if g1.SessionID != a1.SessionID {
		t.Errorf("[0].SessionID = %q; want %q", g1.SessionID, a1.SessionID)
	}
	if g1.TargetCallID != a1.TargetCallID {
		t.Errorf("[0].TargetCallID = %q; want %q", g1.TargetCallID, a1.TargetCallID)
	}
	if g1.Label != a1.Label {
		t.Errorf("[0].Label = %q; want %q", g1.Label, a1.Label)
	}
	if g1.Comment != a1.Comment {
		t.Errorf("[0].Comment = %q; want %q", g1.Comment, a1.Comment)
	}
	if g1.Annotator != a1.Annotator {
		t.Errorf("[0].Annotator = %q; want %q", g1.Annotator, a1.Annotator)
	}
	if g1.Score == nil {
		t.Error("[0].Score is nil; want non-nil")
	} else if *g1.Score != score1 {
		t.Errorf("[0].Score = %f; want %f", *g1.Score, score1)
	}
	if g1.SchemaVersion != 1 {
		t.Errorf("[0].SchemaVersion = %d; want 1", g1.SchemaVersion)
	}

	// Check second annotation.
	g2 := got[1]
	if g2.SessionID != a2.SessionID {
		t.Errorf("[1].SessionID = %q; want %q", g2.SessionID, a2.SessionID)
	}
	if g2.TargetTurn != a2.TargetTurn {
		t.Errorf("[1].TargetTurn = %d; want %d", g2.TargetTurn, a2.TargetTurn)
	}
	if g2.Label != a2.Label {
		t.Errorf("[1].Label = %q; want %q", g2.Label, a2.Label)
	}
	if g2.Score != nil {
		t.Errorf("[1].Score should be nil; got %f", *g2.Score)
	}
}
