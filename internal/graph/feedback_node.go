package graph

import "fmt"

// FeedbackNodeSpec describes the single "added" node a feedback report is
// projected into when a catalog sink routes it toward a review queue. Two
// carriers share it (U2, feedback report 01KXD2300AEDR4DKBVGQY7PJT2):
//
//   - the MCP server's feedback.report catalog sink
//     (internal/mcp/graphsrv.routeFeedbackToCatalogSink), which derives
//     Type/Fields from the catalog's own `feedback_routing:` block, and
//   - the web server's POST /api/feedback/local intake
//     (internal/runstatus/server), which derives them from the consumer
//     repo's producer-keyed `feedback_routing:` kitsoki config.
//
// The target Type always lives in the CONSUMER's catalog — kitsoki itself
// never declares feedback node types; routing merely names one.
type FeedbackNodeSpec struct {
	// Type is the node type id to propose (e.g. "portal-feedback").
	Type string
	// Fields lists field ids of the target type to populate, in the
	// taxonomy's declared order. See FeedbackNodeAfter for the mapping.
	Fields []string
	// NodeID is the fresh id for the new node (never a changeset's own
	// cs-<n>, which Propose mints itself).
	NodeID string
	// Title is the human-readable one-liner carried by the node.
	Title string
	// ReportRef is the durable dereference back to the JSONL/markdown
	// record (a report id or idempotency key).
	ReportRef string
	// Kind classifies what this report is about — a bug report, an
	// improvement idea, or general feedback (the producer's own vocabulary,
	// e.g. feedback-core's bug|issue_request|content_comment|question, or
	// the MCP tool's tool_gap|data_gap|doc_gap|bug|other). Set on the node
	// as `kind` whenever non-empty, independent of Fields — see
	// FeedbackNodeAfter.
	Kind string
	// TargetNodeID is the catalog node this report is about, when the
	// carrier reliably knows one (the web intake's context.nodeIds[0] — the
	// node selected in the portal at submit time — or the MCP tool's
	// anchor.node). Empty when no such target is known. See
	// FeedbackNodeAfter.
	TargetNodeID string
}

// FeedbackNodeAfter builds the flat `after` mapping for a feedback node's
// "added" operation. After is a flat mapping (schema/id/title/status/
// visibility + type-specific fields), never a nested {type,fields,edges}
// envelope — see buildNode/fileNode.
//
// Field mapping (judgment call — a routing config names field IDs only,
// never a value template): the FIRST declared field carries the report's
// title/summary text, so any taxonomy gets at least a human-readable line;
// a field literally named "report" or "report_id" (if the taxonomy declares
// one) additionally gets ReportRef verbatim, for a durable dereference back
// to the JSONL/markdown record. `kind` is set unconditionally (not gated by
// Fields, which only names value-templated fields) whenever the carrier
// supplied one, since it's intrinsic to the report the same way ReportRef
// is.
//
// Edge target: when the carrier supplied a TargetNodeID (the web intake's
// context.nodeIds[0], or the MCP tool's anchor.node) the node's
// filed_against edge is populated directly — the whole point of capturing
// it is that the submitter's selection IS the reliable target a feedback
// submission can supply. An unknown or stale id is not specially validated
// here; if the target type requires the edge and this id doesn't resolve,
// lint surfaces it as a normal, honest validation gap on the proposal, same
// as any other malformed edge. Absent a TargetNodeID (e.g. nothing was
// selected, or the MCP caller passed no anchor.node), the node lands with
// no filed_against target for a human to fill in during review — unchanged
// from before.
func FeedbackNodeAfter(spec FeedbackNodeSpec) map[string]any {
	after := map[string]any{
		"schema":     fmt.Sprintf("graph/%s/v0", spec.Type),
		"id":         spec.NodeID,
		"title":      spec.Title,
		"status":     "draft",
		"visibility": "internal",
	}
	for i, f := range spec.Fields {
		if i == 0 {
			after[f] = spec.Title
		}
		if f == "report" || f == "report_id" {
			after[f] = spec.ReportRef
		}
	}
	if spec.Kind != "" {
		after["kind"] = spec.Kind
	}
	if spec.TargetNodeID != "" {
		after["edges"] = map[string]any{"filed_against": []string{spec.TargetNodeID}}
	}
	return after
}
