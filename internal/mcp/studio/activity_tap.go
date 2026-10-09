package studio

// activity_tap.go — live in-flight visibility for async drives (never-silent).
//
// While session.submit/drive runs a turn asynchronously, session.status used to
// report only the resting state captured at submit time (e.g. "bf.idle") plus a
// bare running handle. An MCP operator polling status saw the same resting
// state on every poll and concluded the run was stuck — even while a worker
// agent was actively streaming inside the turn — and killed healthy runs.
//
// The tap wraps the session's JSONL event sink and remembers the most recent
// event's state path, kind, timestamp, and (sticky within the turn) the last
// dispatched agent, so status/inspect can report what the running turn is
// actually doing without touching the orchestrator's locks.

import (
	"encoding/json"
	"sync"
	"time"

	"kitsoki/internal/store"
)

// inFlightEvent is the most recent trace activity observed by the tap.
type inFlightEvent struct {
	statePath string
	kind      string
	ts        time.Time
	agent     string
}

// activityTap records the latest event written through the session's sink.
type activityTap struct {
	mu   sync.Mutex
	last inFlightEvent
	seen bool
}

func (t *activityTap) record(ev store.Event) {
	if t == nil {
		return
	}
	ts := ev.Ts
	if ts.IsZero() {
		// Events are timestamped downstream by the sink; observation time is
		// the honest activity signal here.
		ts = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	prevAgent := t.last.agent
	t.last = inFlightEvent{
		statePath: string(ev.StatePath),
		kind:      string(ev.Kind),
		ts:        ts,
		agent:     prevAgent, // sticky: keep the active agent across stream events
	}
	t.seen = true
	switch ev.Kind {
	case store.AgentCalled:
		var p struct {
			Agent string `json:"agent"`
		}
		if len(ev.Payload) > 0 && json.Unmarshal(ev.Payload, &p) == nil && p.Agent != "" {
			t.last.agent = p.Agent
		}
	case store.AgentReturned, store.AgentError:
		t.last.agent = ""
	}
}

// snapshot returns the latest activity, if any event has been observed.
func (t *activityTap) snapshot() (inFlightEvent, bool) {
	if t == nil {
		return inFlightEvent{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last, t.seen
}

// tapSink wraps an EventSink, recording each event into the tap before
// delegating. History() passes through via embedding.
type tapSink struct {
	store.EventSink
	tap *activityTap
}

func (s tapSink) Append(ev store.Event) error {
	s.tap.record(ev)
	return s.EventSink.Append(ev)
}
