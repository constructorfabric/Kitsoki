// Package proposalsadapter is W6.0's pilot adapter: it renders a proposal
// node's docs/proposals/README.md "Current proposals" index entry from
// graph data, so that index can stop being hand-maintained (the
// machine-checkable-docs principle).
//
// Per the W6.0 design session: unlike W3.0's featuresadapter (which checks
// semantic equivalence, because features/*.yaml is structured data reread
// by other tools), the README index is a human-facing generated markdown
// artifact with no downstream re-parser — its exact bytes ARE the contract,
// so drift-checking here is a byte comparison. That also means this
// package intentionally emits one unwrapped line per entry rather than
// trying to reproduce hand-wrapped prose's arbitrary line width — a
// generator that has to byte-match arbitrary human wrapping decisions is
// exactly the fragility a generated index exists to avoid.
package proposalsadapter

import (
	"fmt"
	"strings"

	"kitsoki/internal/graph"
)

// RenderIndexEntry renders proposalNode's docs/proposals/README.md
// "Current proposals" entry: `- [\`path\`](path) — **kind.** blurb`.
// path is proposalNode's `path` field with the docs/proposals/ prefix
// stripped (README entries link relative to their own directory).
func RenderIndexEntry(proposalNode *graph.Node) (string, error) {
	path, _ := proposalNode.Fields["path"].(string)
	if path == "" {
		return "", fmt.Errorf("proposalsadapter: node %q missing \"path\"", proposalNode.ID)
	}
	relPath := strings.TrimPrefix(path, "docs/proposals/")

	kind, _ := proposalNode.Fields["proposal_kind"].(string)
	if kind == "" {
		return "", fmt.Errorf("proposalsadapter: node %q missing \"proposal_kind\"", proposalNode.ID)
	}

	blurb, _ := proposalNode.Fields["index_blurb"].(string)
	if blurb == "" {
		return "", fmt.Errorf("proposalsadapter: node %q missing \"index_blurb\"", proposalNode.ID)
	}

	return fmt.Sprintf("- [`%s`](%s) — **%s.** %s", relPath, relPath, kind, blurb), nil
}

// GraphSourcedProposals returns every top-level `proposal`-typed node in cat
// that carries the fields RenderIndexEntry needs (index_blurb) — i.e. the
// proposals that have actually been converted so far. Wrapper-first
// migration means most proposals in docs/proposals/ are NOT yet graph
// nodes at all; this only reports the ones that are, so drift-checking
// stays scoped to what's actually been converted. Nodes with a `child_of`
// edge are nested child proposals, not top-level index entries — see
// GraphSourcedChildProposals.
func GraphSourcedProposals(cat *graph.Catalog) []*graph.Node {
	var proposals []*graph.Node
	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]
		if node.TypeID != "proposal" {
			continue
		}
		if _, ok := node.Fields["index_blurb"]; !ok {
			continue
		}
		if childOfEdge(node) != "" {
			continue
		}
		proposals = append(proposals, node)
	}
	return proposals
}

// GraphSourcedChildProposals returns every `proposal`-typed node in cat that
// is a nested child of another proposal (via `child_of`) and carries the
// fields RenderChildEntry needs. These render as an indented sub-bullet
// under their parent's README entry rather than a top-level bullet.
func GraphSourcedChildProposals(cat *graph.Catalog) []*graph.Node {
	var children []*graph.Node
	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]
		if node.TypeID != "proposal" {
			continue
		}
		if _, ok := node.Fields["index_blurb"]; !ok {
			continue
		}
		if childOfEdge(node) == "" {
			continue
		}
		children = append(children, node)
	}
	return children
}

func childOfEdge(node *graph.Node) string {
	targets, ok := node.Edges["child_of"]
	if !ok || len(targets) == 0 {
		return ""
	}
	return string(targets[0])
}

// RenderChildEntry renders a nested child proposal's docs/proposals/README.md
// sub-bullet: `  - [\`path\`](path) (kind[, note]) — blurb`, indented two
// spaces under its parent's top-level entry. child_note is an optional extra
// parenthetical (e.g. "partially implemented"); omitted when blank.
func RenderChildEntry(node *graph.Node) (string, error) {
	path, _ := node.Fields["path"].(string)
	if path == "" {
		return "", fmt.Errorf("proposalsadapter: node %q missing \"path\"", node.ID)
	}
	relPath := strings.TrimPrefix(path, "docs/proposals/")

	kind, _ := node.Fields["proposal_kind"].(string)
	if kind == "" {
		return "", fmt.Errorf("proposalsadapter: node %q missing \"proposal_kind\"", node.ID)
	}

	blurb, _ := node.Fields["index_blurb"].(string)
	if blurb == "" {
		return "", fmt.Errorf("proposalsadapter: node %q missing \"index_blurb\"", node.ID)
	}

	paren := kind
	if note, _ := node.Fields["child_note"].(string); note != "" {
		paren = kind + ", " + note
	}

	return fmt.Sprintf("  - [`%s`](%s) (%s) — %s", relPath, relPath, paren, blurb), nil
}
