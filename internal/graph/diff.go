// diff.go — W4.0: the computed roadmap. Per the epic's Shared decision 4
// ("roadmap is computed, never authored"), Diff is the only thing that
// produces roadmap work: desired-state nodes and changesets are authored,
// the roadmap itself is a pure view over the gap between a current-state
// and a desired-state catalog.
package graph

import (
	"fmt"
	"reflect"
	"sort"

	"gopkg.in/yaml.v3"
)

// ChangeNode is the schemas/change-node.schema.json-conforming shape Diff
// emits, so goal.py-style ledgers, stories/deliver, and stories/fleet
// consume the computed roadmap unchanged — the same contract an authored
// decomposition.yaml change node uses.
type ChangeNode struct {
	ID         string
	Title      string
	Goal       string
	Scope      []string
	Acceptance []string
	DependsOn  []string
}

// gapKind classifies why a node id is part of the roadmap gap.
type gapKind int

const (
	gapAdded gapKind = iota
	gapModified
	gapRemoved
)

// GapKind is the exported, wire/UI-friendly form of a node's diff
// classification (see NodeDiff).
type GapKind string

const (
	GapAdded    GapKind = "added"
	GapModified GapKind = "modified"
	GapRemoved  GapKind = "removed"
)

func (k gapKind) exported() GapKind {
	switch k {
	case gapAdded:
		return GapAdded
	case gapRemoved:
		return GapRemoved
	default:
		return GapModified
	}
}

// NodeDiff is one gapped node id's classification plus both sides of the
// comparison, for callers that render the diff itself (e.g. a UI diff mode)
// rather than the derived roadmap ChangeNode list. Current is nil when Kind
// is GapAdded; Desired is nil when Kind is GapRemoved.
type NodeDiff struct {
	ID      NodeID
	Kind    GapKind
	Current *Node
	Desired *Node
}

// DiffNodes classifies every node id present in current XOR desired, plus
// every id present in both whose STRUCTURAL fields differ (see
// structurallyDiffers) — the same gap Diff renders as a roadmap ChangeNode
// list, exposed at node granularity for callers that need the raw
// before/after (a UI diff mode) rather than a derived change-node brief.
// Output is sorted by id for determinism.
func DiffNodes(current, desired *Catalog) []NodeDiff {
	sorted, gapped := gapClassify(current, desired)
	diffs := make([]NodeDiff, 0, len(gapped))
	for _, id := range sorted {
		kind, isGap := gapped[id]
		if !isGap {
			continue
		}
		diffs = append(diffs, NodeDiff{
			ID:      id,
			Kind:    kind.exported(),
			Current: current.Nodes[id],
			Desired: desired.Nodes[id],
		})
	}
	return diffs
}

// gapClassify is the shared gap computation Diff and DiffNodes both build
// on, so the roadmap and the node-level diff can never disagree about which
// ids are gapped or why.
func gapClassify(current, desired *Catalog) ([]NodeID, map[NodeID]gapKind) {
	ids := map[NodeID]bool{}
	for id := range current.Nodes {
		ids[id] = true
	}
	for id := range desired.Nodes {
		ids[id] = true
	}
	sorted := make([]NodeID, 0, len(ids))
	for id := range ids {
		sorted = append(sorted, id)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	gapped := map[NodeID]gapKind{}
	for _, id := range sorted {
		c, inCurrent := current.Nodes[id]
		d, inDesired := desired.Nodes[id]
		switch {
		case inDesired && !inCurrent:
			gapped[id] = gapAdded
		case inCurrent && !inDesired:
			gapped[id] = gapRemoved
		case structurallyDiffers(c, d):
			gapped[id] = gapModified
		}
	}
	return sorted, gapped
}

// Diff computes the roadmap: every gapClassify gap rendered as a
// schemas/change-node.schema.json-conforming ChangeNode, so goal.py-style
// ledgers, stories/deliver, and stories/fleet consume the computed roadmap
// unchanged.
//
// depends_on on an emitted change node is projected to only the OTHER gap
// ids among desired's depends_on for that node — a dependency already
// satisfied in current is dropped, since there's no roadmap work left for
// it. This makes the emitted depends_on DAG a closed subgraph of desired's
// own (already-acyclic) dependency graph, so acyclicity is free.
func Diff(current, desired *Catalog) []ChangeNode {
	sorted, gapped := gapClassify(current, desired)

	var changes []ChangeNode
	for _, id := range sorted {
		kind, isGap := gapped[id]
		if !isGap {
			continue
		}
		switch kind {
		case gapAdded:
			changes = append(changes, buildChangeNode("add", desired.Nodes[id], gapped))
		case gapModified:
			changes = append(changes, buildChangeNode("modify", desired.Nodes[id], gapped))
		case gapRemoved:
			changes = append(changes, buildRemovedChangeNode(current.Nodes[id]))
		}
	}
	return changes
}

// structurallyDiffers compares everything about c and d except Status.
func structurallyDiffers(c, d *Node) bool {
	if c.Title != d.Title || c.Visibility != d.Visibility {
		return true
	}
	if !reflect.DeepEqual(c.Sources, d.Sources) {
		return true
	}
	if !reflect.DeepEqual(c.Edges, d.Edges) {
		return true
	}
	return !reflect.DeepEqual(c.Fields, d.Fields)
}

func buildChangeNode(verb string, node *Node, gapped map[NodeID]gapKind) ChangeNode {
	return ChangeNode{
		ID:         verb + "-" + string(node.ID),
		Title:      verbTitle(verb, node.Title),
		Goal:       stringFieldOr(node.Fields, "goal", node.Title),
		Scope:      stringSliceField(node.Fields, "scope"),
		Acceptance: stringSliceField(node.Fields, "acceptance"),
		DependsOn:  projectDependsOn(node, gapped),
	}
}

func buildRemovedChangeNode(node *Node) ChangeNode {
	return ChangeNode{
		ID:         "remove-" + string(node.ID),
		Title:      verbTitle("remove", node.Title),
		Goal:       fmt.Sprintf("Remove %s: present in the current graph, no longer wanted in the desired graph.", node.ID),
		Scope:      stringSliceField(node.Fields, "scope"),
		Acceptance: []string{fmt.Sprintf("%s no longer exists in the catalog", node.ID)},
		DependsOn:  nil,
	}
}

func verbTitle(verb, title string) string {
	switch verb {
	case "add":
		return "Add: " + title
	case "modify":
		return "Modify: " + title
	case "remove":
		return "Remove: " + title
	default:
		return title
	}
}

// projectDependsOn keeps only the ids in node's desired depends_on that are
// themselves part of the gap — a dependency already satisfied in current
// has no roadmap work left, so it's dropped rather than emitted as a
// depends_on the consumer would wait on forever.
func projectDependsOn(node *Node, gapped map[NodeID]gapKind) []string {
	raw, _ := node.Fields["depends_on"].([]any)
	var kept []string
	for _, r := range raw {
		s, ok := r.(string)
		if !ok {
			continue
		}
		if _, isGap := gapped[NodeID(s)]; isGap {
			kept = append(kept, prefixForGap(NodeID(s), gapped)+s)
		}
	}
	return kept
}

// prefixForGap returns the verb prefix the OTHER gapped node's own emitted
// ChangeNode.ID uses, so a depends_on entry names a real emitted id.
func prefixForGap(id NodeID, gapped map[NodeID]gapKind) string {
	switch gapped[id] {
	case gapAdded:
		return "add-"
	case gapModified:
		return "modify-"
	case gapRemoved:
		return "remove-"
	default:
		return ""
	}
}

func stringFieldOr(fields map[string]any, key, fallback string) string {
	if s, ok := fields[key].(string); ok {
		return s
	}
	return fallback
}

func stringSliceField(fields map[string]any, key string) []string {
	raw, _ := fields[key].([]any)
	if raw == nil {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// changeNodeDoc is the on-disk shape for MarshalRoadmap — the required
// fields from schemas/change-node.schema.json (id/title/goal/scope/
// acceptance/depends_on), which is additionalProperties:true so this
// minimal set is a valid change node.
type changeNodeDoc struct {
	ID         string   `yaml:"id"`
	Title      string   `yaml:"title"`
	Goal       string   `yaml:"goal"`
	Scope      []string `yaml:"scope"`
	Acceptance []string `yaml:"acceptance"`
	DependsOn  []string `yaml:"depends_on"`
}

// MarshalRoadmap serializes changes under a top-level `briefs:` key —
// stories/deliver's lint_decomposition.star and schemas/change-node.schema.json
// both expect that key.
func MarshalRoadmap(changes []ChangeNode) ([]byte, error) {
	docs := make([]changeNodeDoc, len(changes))
	for i, c := range changes {
		docs[i] = changeNodeDoc{
			ID: c.ID, Title: c.Title, Goal: c.Goal,
			Scope: c.Scope, Acceptance: c.Acceptance, DependsOn: c.DependsOn,
		}
	}
	return yaml.Marshal(map[string]any{"briefs": docs})
}
