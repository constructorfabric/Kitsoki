package kitworklist

import (
	"path/filepath"
	"testing"

	"kitsoki/internal/kitstage"
)

func TestItemIDStableAcrossVolatileDetail(t *testing.T) {
	a := ItemID("load", "/Users/alice/proj/.kitsoki/stories/x/app.yaml", "app.yaml:12:3: does not list child host \"host.local\"")
	b := ItemID("load", "/home/bob/other/.kitsoki/stories/x/app.yaml", "app.yaml:99:7: does not list child host \"host.local\"")
	if a != b {
		t.Fatalf("ids should survive path + line/column churn: %q vs %q", a, b)
	}
	c := ItemID("load", "app.yaml", "does not list child host \"host.other\"")
	if a == c {
		t.Fatal("genuinely different failures must get different ids")
	}
	d := ItemID("flow", "app.yaml", "does not list child host \"host.local\"")
	if a == d {
		t.Fatal("different kinds must get different ids")
	}
}

func TestItemIDStableAcrossFragmentOrder(t *testing.T) {
	// Producers that assemble a detail from map iteration (e.g. the flow
	// runner's expect_world sweep) emit "; "-joined fragments in varying
	// order; the id must not flip with them or waivers never carry forward.
	a := ItemID("flow", "fix.yaml", `expect_state: got "landing", want "plan_done"; expect_world["verify_ok"]: got  (string), want true (bool); expect_world["captured"]: got 0 (uint64), want 1 (uint64)`)
	b := ItemID("flow", "fix.yaml", `expect_state: got "landing", want "plan_done"; expect_world["captured"]: got 0 (uint64), want 1 (uint64); expect_world["verify_ok"]: got  (string), want true (bool)`)
	if a != b {
		t.Fatalf("ids should survive fragment reordering: %q vs %q", a, b)
	}
	c := ItemID("flow", "fix.yaml", `expect_state: got "landing", want "plan_done"; expect_world["verify_ok"]: got false (bool), want true (bool)`)
	if a == c {
		t.Fatal("genuinely different fragment sets must get different ids")
	}
}

func TestMergeCarryForwardAndResolution(t *testing.T) {
	stale := NewItem("load", SeverityError, "app.yaml", "old failure now fixed")
	waived := NewItem("flow", SeverityError, "fix.yaml", "known flaky expectation")
	noted := NewItem("rename", SeverityWarn, "app.yaml", "bump -> nudge")
	prev := &File{Items: []Item{
		func() Item { it := stale; return it }(),
		func() Item { it := waived; it.Status = StatusAccepted; it.Notes = "accepted for v2"; return it }(),
		func() Item { it := noted; it.Notes = "will re-freeze after accept"; return it }(),
	}}

	fresh := []Item{
		NewItem("flow", SeverityError, "fix.yaml", "known flaky expectation"), // reproduced, was waived
		NewItem("rename", SeverityWarn, "app.yaml", "bump -> nudge"),          // reproduced, had notes
		NewItem("contract", SeverityError, "widget", "new failure"),           // brand new
	}

	merged := Merge(fresh, prev)
	byID := map[string]Item{}
	for _, it := range merged {
		byID[it.ID] = it
	}

	if got := byID[stale.ID]; got.Status != StatusResolved {
		t.Errorf("vanished item should flip resolved, got %q", got.Status)
	}
	if got := byID[waived.ID]; got.Status != StatusAccepted || got.Notes != "accepted for v2" {
		t.Errorf("reproduced waiver should carry forward, got %+v", got)
	}
	if got := byID[noted.ID]; got.Status != StatusOpen || got.Notes != "will re-freeze after accept" {
		t.Errorf("reproduced item should keep notes and stay open, got %+v", got)
	}

	f := &File{Items: merged}
	if f.OpenErrors() != 1 {
		t.Errorf("only the brand-new error should block, OpenErrors = %d", f.OpenErrors())
	}
	open, resolved, accepted := f.Counts()
	if open != 2 || resolved != 1 || accepted != 1 {
		t.Errorf("counts = (%d,%d,%d), want (2 open [error+warn], 1 resolved, 1 accepted)", open, resolved, accepted)
	}
}

func TestMergeReopensRegressions(t *testing.T) {
	item := NewItem("flow", SeverityError, "fix.yaml", "failure")
	resolved := item
	resolved.Status = StatusResolved
	prev := &File{Items: []Item{resolved}}

	merged := Merge([]Item{NewItem("flow", SeverityError, "fix.yaml", "failure")}, prev)
	if len(merged) != 1 || merged[0].Status != StatusOpen {
		t.Fatalf("a reproduced previously-resolved item must reopen, got %+v", merged)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".kitsoki", "kit-update", "widget", FileName)
	f := &File{
		Schema: Schema, Kit: "widget",
		From:  kitstage.Snapshot{Version: "1.2.0"},
		To:    kitstage.Snapshot{Version: "1.3.0"},
		Items: []Item{NewItem("load", SeverityError, "app.yaml", "boom")},
	}
	if err := Save(path, f); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got.Items) != 1 || got.Items[0].ID != f.Items[0].ID {
		t.Fatalf("round trip mangled worklist: %+v", got)
	}
	if missing, err := Load(filepath.Join(dir, "nope.yaml")); err != nil || missing != nil {
		t.Fatalf("missing file should load as (nil,nil), got (%v,%v)", missing, err)
	}
}
