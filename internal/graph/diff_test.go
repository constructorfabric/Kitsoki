package graph

import (
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestDiff_GoldenRoadmap is W4.0's gate: a fixture pair (current, desired)
// whose computed delta equals a golden work-graph. It exercises every
// branch: identical (no gap), status-only differs (no gap — status is
// process state, not roadmap work), goal differs (modified), new in
// desired (added), and present only in current (removed) — plus the
// depends_on projection dropping deps already satisfied in current.
func TestDiff_GoldenRoadmap(t *testing.T) {
	current, err := LoadCatalog("testdata/diff/current")
	if err != nil {
		t.Fatalf("load current: %v", err)
	}
	desired, err := LoadCatalog("testdata/diff/desired")
	if err != nil {
		t.Fatalf("load desired: %v", err)
	}

	got := Diff(current, desired)

	want := []ChangeNode{
		{
			ID:         "add-change-diff",
			Title:      "Add: internal/graph diff",
			Goal:       "Compute the roadmap delta between current and desired catalogs.",
			Scope:      []string{"internal/graph/diff.go"},
			Acceptance: []string{"go test ./internal/graph/... green"},
			DependsOn:  []string{"modify-change-modify-target"},
		},
		{
			ID:         "remove-change-legacy-cli",
			Title:      "Remove: legacy graph CLI verb",
			Goal:       "Remove change-legacy-cli: present in the current graph, no longer wanted in the desired graph.",
			Scope:      []string{"cmd/kitsoki/legacy.go"},
			Acceptance: []string{"change-legacy-cli no longer exists in the catalog"},
			DependsOn:  nil,
		},
		{
			ID:         "modify-change-modify-target",
			Title:      "Modify: internal/graph modify target",
			Goal:       "Sharpen modify to reject stale field-change paths, not just stale values.",
			Scope:      []string{"internal/graph/modify.go"},
			Acceptance: []string{"go test ./internal/graph/... green"},
			DependsOn:  nil,
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Diff mismatch.\ngot:  %+v\nwant: %+v", got, want)
	}
}

func TestDiff_NoGapWhenCatalogsIdentical(t *testing.T) {
	current, err := LoadCatalog("testdata/diff/current")
	if err != nil {
		t.Fatalf("load current: %v", err)
	}
	if got := Diff(current, current); len(got) != 0 {
		t.Errorf("Diff(current, current) = %v, want no gap", got)
	}
}

func TestMarshalRoadmap_RoundTrips(t *testing.T) {
	current, err := LoadCatalog("testdata/diff/current")
	if err != nil {
		t.Fatalf("load current: %v", err)
	}
	desired, err := LoadCatalog("testdata/diff/desired")
	if err != nil {
		t.Fatalf("load desired: %v", err)
	}
	changes := Diff(current, desired)

	raw, err := MarshalRoadmap(changes)
	if err != nil {
		t.Fatalf("MarshalRoadmap: %v", err)
	}

	var doc struct {
		Briefs []struct {
			ID         string   `yaml:"id"`
			Title      string   `yaml:"title"`
			Goal       string   `yaml:"goal"`
			Scope      []string `yaml:"scope"`
			Acceptance []string `yaml:"acceptance"`
			DependsOn  []string `yaml:"depends_on"`
		} `yaml:"briefs"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal MarshalRoadmap output: %v", err)
	}
	if len(doc.Briefs) != len(changes) {
		t.Fatalf("briefs has %d entries, want %d", len(doc.Briefs), len(changes))
	}
	for i, b := range doc.Briefs {
		if b.ID != changes[i].ID {
			t.Errorf("briefs[%d].id = %q, want %q", i, b.ID, changes[i].ID)
		}
		if len(b.Scope) == 0 {
			t.Errorf("briefs[%d].scope is empty, want non-empty (schema requires minItems 1)", i)
		}
		if len(b.Acceptance) == 0 {
			t.Errorf("briefs[%d].acceptance is empty, want non-empty (schema requires minItems 1)", i)
		}
	}
}

// TestDiffNodes_MatchesDiffGap exercises the node-granularity classification
// a UI diff mode needs against the same golden fixture pair TestDiff_GoldenRoadmap
// uses, and checks it agrees with Diff's own gap set (added/modified/removed
// ids) since gapClassify backs both.
func TestDiffNodes_MatchesDiffGap(t *testing.T) {
	current, err := LoadCatalog("testdata/diff/current")
	if err != nil {
		t.Fatalf("load current: %v", err)
	}
	desired, err := LoadCatalog("testdata/diff/desired")
	if err != nil {
		t.Fatalf("load desired: %v", err)
	}

	nodeDiffs := DiffNodes(current, desired)
	got := map[NodeID]GapKind{}
	for _, d := range nodeDiffs {
		got[d.ID] = d.Kind
	}
	want := map[NodeID]GapKind{
		"change-diff":          GapAdded,
		"change-legacy-cli":    GapRemoved,
		"change-modify-target": GapModified,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DiffNodes gap kinds = %+v, want %+v", got, want)
	}

	for _, d := range nodeDiffs {
		switch d.Kind {
		case GapAdded:
			if d.Current != nil || d.Desired == nil {
				t.Errorf("added NodeDiff %q: want Current=nil, Desired!=nil, got Current=%v Desired=%v", d.ID, d.Current, d.Desired)
			}
		case GapRemoved:
			if d.Desired != nil || d.Current == nil {
				t.Errorf("removed NodeDiff %q: want Desired=nil, Current!=nil, got Current=%v Desired=%v", d.ID, d.Current, d.Desired)
			}
		case GapModified:
			if d.Current == nil || d.Desired == nil {
				t.Errorf("modified NodeDiff %q: want both Current and Desired non-nil, got Current=%v Desired=%v", d.ID, d.Current, d.Desired)
			}
		}
	}
}
