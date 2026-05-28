// Package runstatus defines the canonical Snapshot type shared across the
// run-status feature: the JSON-RPC method returns (live mode), the
// self-contained HTML artifact (export-status), and test fixtures all use
// the same shape.
package runstatus

import (
	"encoding/json"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/viz"
)

// Snapshot is the canonical, self-contained representation of a kitsoki session
// at a point in time. It is the shape that:
//   - kitsoki export-status --from-trace inlines into the HTML artifact (Phase 1),
//   - runstatus.session.get / runstatus.session.trace return in live mode (Phase 3),
//   - test fixtures under tools/runstatus/fixtures/ are authored against.
type Snapshot struct {
	Session SessionHeader   `json:"session"`
	App     *app.AppDef     `json:"app"`
	Mermaid MermaidSnapshot `json:"mermaid"`
	Events  []TraceEvent    `json:"events"`
}

// SessionHeader carries the session-level metadata shown in the UI header.
// It maps 1:1 onto the runstatus.sessions.list row shape so navigation code
// can reuse it without a secondary fetch.
type SessionHeader struct {
	SessionID    string    `json:"session_id"`
	AppID        string    `json:"app_id"`
	CurrentState string    `json:"current_state"`
	Turn         int       `json:"turn"`
	StartedAt    time.Time `json:"started_at"`
	Terminal     bool      `json:"terminal"`
}

// MermaidSnapshot is the output of (the future) viz.FlowchartWithMap: the
// Mermaid LR source plus a sidecar that maps every diagram node-ID back to
// an AppDef path so clicking a node can open the detail drawer.
type MermaidSnapshot struct {
	Source  string             `json:"source"`
	NodeMap map[string]NodeRef `json:"node_map"`
}

// NodeRef identifies the AppDef entity that a Mermaid diagram node represents.
// Aliased to viz.NodeRef so the viz emitter and the snapshot consumers share
// one type. See internal/viz/nodemap.go for the encoding of Ref strings.
type NodeRef = viz.NodeRef

// TraceEvent is one slog record from the JSONL trace file, parsed into a
// typed shape. The well-known slog keys (time, level, msg, session_id, turn,
// state_path) are promoted to named fields; any other key-value pair lands
// in Attrs so no information is lost.
//
// UnmarshalJSON implements the "known fields promoted, rest into Attrs" logic.
// When marshalling back to JSON all fields are emitted normally.
type TraceEvent struct {
	Time       time.Time      `json:"time"`
	Level      string         `json:"level"`
	Msg        string         `json:"msg"`
	SessionID  string         `json:"session_id"`
	Turn       int            `json:"turn"`
	StatePath  string         `json:"state_path"`
	// ParentTurn is non-zero for off-path event batches: it holds the
	// foreground turn that was active when the off-path interaction occurred.
	// The trace UI uses this to render off-path groups as sub-items of their
	// parent turn rather than as independent sibling turns.
	ParentTurn int            `json:"parent_turn,omitempty"`
	Attrs      map[string]any `json:"attrs"` // catch-all for slog kv pairs not modelled above
}

// knownTraceKeys is the set of top-level JSON keys that are promoted to named
// fields on TraceEvent. Everything else lands in Attrs.
//
// "attrs" is also listed here so that a previously serialised TraceEvent
// (where extra fields were stored under the "attrs" key) round-trips cleanly:
// the UnmarshalJSON handler deserialises the "attrs" object into Attrs
// directly rather than nesting it under Attrs["attrs"].
var knownTraceKeys = map[string]bool{
	"time":        true,
	"level":       true,
	"msg":         true,
	"session_id":  true,
	"turn":        true,
	"state_path":  true,
	"parent_turn": true,
	"attrs":       true, // handled explicitly below for round-trip safety
}

// UnmarshalJSON implements json.Unmarshaler.
// It decodes the well-known slog keys into the typed fields and routes any
// remaining keys (event-specific payloads like `intent`, `handler`, etc.) into
// the Attrs map so the full record is preserved for the UI.
func (e *TraceEvent) UnmarshalJSON(b []byte) error {
	// Decode everything into a raw map first.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}

	// Promote known fields.
	if v, ok := raw["time"]; ok {
		var s string
		if err := json.Unmarshal(v, &s); err == nil {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err == nil {
				e.Time = t
			}
		}
	}
	if v, ok := raw["level"]; ok {
		_ = json.Unmarshal(v, &e.Level)
	}
	if v, ok := raw["msg"]; ok {
		_ = json.Unmarshal(v, &e.Msg)
	}
	if v, ok := raw["session_id"]; ok {
		_ = json.Unmarshal(v, &e.SessionID)
	}
	if v, ok := raw["turn"]; ok {
		_ = json.Unmarshal(v, &e.Turn)
	}
	if v, ok := raw["state_path"]; ok {
		_ = json.Unmarshal(v, &e.StatePath)
	}
	if v, ok := raw["parent_turn"]; ok {
		_ = json.Unmarshal(v, &e.ParentTurn)
	}

	// "attrs" is the serialised form of a previously marshalled TraceEvent.
	// Merge its entries directly into e.Attrs so a JSON round-trip does not
	// produce double-nesting (Attrs["attrs"]["key"] instead of Attrs["key"]).
	if v, ok := raw["attrs"]; ok {
		var nested map[string]any
		if err := json.Unmarshal(v, &nested); err == nil && len(nested) > 0 {
			if e.Attrs == nil {
				e.Attrs = make(map[string]any, len(nested))
			}
			for k, val := range nested {
				e.Attrs[k] = val
			}
		}
	}

	// Collect remaining unknown keys into Attrs.
	for k, v := range raw {
		if knownTraceKeys[k] {
			continue
		}
		var val any
		if err := json.Unmarshal(v, &val); err == nil {
			if e.Attrs == nil {
				e.Attrs = make(map[string]any)
			}
			e.Attrs[k] = val
		}
	}
	return nil
}
