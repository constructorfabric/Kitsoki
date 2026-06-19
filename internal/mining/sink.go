package mining

import (
	"encoding/json"
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// EventSink is the seam the mining loop records its two events through. In
// production this is satisfied by an adapter over the orchestrator's
// side-channel append (the same off-path path GateDecided uses); in tests it is
// an in-memory recorder. Kept narrow so the package never imports the
// orchestrator.
type EventSink interface {
	// AppendMiningEvent appends one mining event for the session. The event's
	// Kind is one of store.MiningProposal{Raised,Decided} and its Payload is the
	// marshaled typed payload. Implementations attach turn/seq/timestamp.
	AppendMiningEvent(sid app.SessionID, kind store.EventKind, payload json.RawMessage) error
}

// SessionSink binds an EventSink to a single session so the proposer/apply gate
// — which are per-chat — need not thread a session id through every call. It is
// the concrete sink the loop passes to Propose / Apply (nil to skip recording).
type SessionSink struct {
	SID  app.SessionID
	Sink EventSink
}

// appendPayload marshals a typed payload and forwards it to the bound session.
func appendPayload(s *SessionSink, kind store.EventKind, payload any) error {
	if s == nil || s.Sink == nil {
		return nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mining: marshal %s payload: %w", kind, err)
	}
	return s.Sink.AppendMiningEvent(s.SID, kind, raw)
}
