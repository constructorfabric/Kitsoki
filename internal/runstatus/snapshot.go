package runstatus

import (
	"encoding/json"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/viz"
)

// Snapshot is the canonical, self-contained representation of a kitsoki session
// at a point in time. One shape feeds three consumers — the export-status HTML
// artifact, the live runstatus.session.get / runstatus.session.trace RPC
// returns, and the test fixtures under tools/runstatus/fixtures/ — so the live
// and exported views cannot drift apart.
//
// A Snapshot is a plain value with no constructor of its own; build it via
// [FromHistory] or [FromSink]. It is safe for concurrent reads, but the
// TraceEvent.Attrs and Mermaid.NodeMap maps it carries are shared with the
// builder's working state, not deep copied — treat a returned Snapshot as
// read-only.
type Snapshot struct {
	Session SessionHeader   `json:"session"`
	App     *app.AppDef     `json:"app"`
	Mermaid MermaidSnapshot `json:"mermaid"`
	Events  []TraceEvent    `json:"events"`

	// Annotations holds the operator scores and labels from the sidecar JSONL
	// file (see [AnnotationPath]). It is nil (omitted from JSON) when no sidecar
	// exists or when the snapshot was built without access to the session dir
	// (e.g. in-memory test snapshots). The SPA uses it to render score/label
	// badges on trace event rows.
	Annotations []Annotation `json:"annotations,omitempty"`

	// RawLines holds the original JSONL bytes for each event in Events, one
	// entry per event, in the same order. RawLines[i] is the raw marshalled line
	// (without trailing newline) that would appear on disk for Events[i].
	//
	// RawLines captures the original JSONL bytes so tests can assert
	// byte-equality against the source trace file (joining the lines with
	// newlines must reproduce the file's event section), and so the SPA's
	// "view raw" feature can show the on-disk line verbatim. It is not
	// serialised (json:"-"): it exists only as a fidelity check and a raw-view
	// source, never as part of the wire/HTML payload. The exporter pass-through
	// guarantee that motivates it is documented in docs/tracing/trace-format.md.
	RawLines [][]byte `json:"-"`
}

// SessionHeader carries the session-level metadata shown in the UI header.
// It maps 1:1 onto the runstatus.sessions.list row shape so navigation code
// can reuse it without a secondary fetch.
type SessionHeader struct {
	SessionID    string               `json:"session_id"`
	AppID        string               `json:"app_id"`
	CurrentState string               `json:"current_state"`
	Turn         int                  `json:"turn"`
	StartedAt    time.Time            `json:"started_at"`
	Terminal     bool                 `json:"terminal"`
	OperationRun *OperationRunSummary `json:"operation_run,omitempty"`
}

// OperationRunSummary is the compact session-level operation handle exposed on
// session list rows. It mirrors the durable `world.operation_run` shape closely
// enough for the Home screen to show attachable autonomous work without fetching
// the full trace for every row.
type OperationRunSummary struct {
	OperationID            string `json:"operation_id,omitempty"`
	PolicyID               string `json:"policy_id,omitempty"`
	Title                  string `json:"title,omitempty"`
	Status                 string `json:"status,omitempty"`
	Mode                   string `json:"mode,omitempty"`
	ExecutionMode          string `json:"execution_mode,omitempty"`
	RunInBackground        bool   `json:"run_in_background,omitempty"`
	From                   string `json:"from,omitempty"`
	To                     string `json:"to,omitempty"`
	EntryIntent            string `json:"entry_intent,omitempty"`
	Phase                  string `json:"phase,omitempty"`
	TerminalState          string `json:"terminal_state,omitempty"`
	TerminalArtifact       string `json:"terminal_artifact,omitempty"`
	TerminalArtifactHandle string `json:"terminal_artifact_handle,omitempty"`
	StopReason             string `json:"stop_reason,omitempty"`
	StopDetail             string `json:"stop_detail,omitempty"`
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
// state_path) are promoted to named fields so the UI can address them without
// string lookups; every other key-value pair lands in Attrs so no information
// from the original record is lost.
//
// The zero TraceEvent is a valid empty event (nil Attrs). A TraceEvent is
// immutable once built and safe for concurrent reads, but its Attrs map is not
// copied on assignment — callers sharing a TraceEvent share its Attrs and must
// not mutate it. See [TraceEvent.UnmarshalJSON] for the decode contract.
type TraceEvent struct {
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	Msg       string    `json:"msg"`
	SessionID string    `json:"session_id"`
	Turn      int       `json:"turn"`
	StatePath string    `json:"state_path"`
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

// UnmarshalJSON decodes the well-known slog keys into the typed fields and
// routes any remaining keys (event-specific payloads like `intent`, `handler`,
// etc.) into Attrs so the full record is preserved for the UI. A previously
// serialised TraceEvent nests its overflow under an "attrs" object; that case
// is merged directly into Attrs rather than under Attrs["attrs"], so a
// marshal/unmarshal round-trip is stable.
//
// It is lenient by design: a malformed individual field (e.g. an unparseable
// time) is skipped, leaving that field at its zero value, rather than failing
// the whole record. An error is returned only when b is not a JSON object at
// all. A nil receiver panics, per the json.Unmarshaler contract.
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
