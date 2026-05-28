// Package tui — routing_observer.go bridges slog routing events into
// the [RoutingChip]'s tea.Msg stream (semantic-routing proposal §8).
//
// Background context: the orchestrator emits structured routing events
// via slog (turn.semantic_hit / miss / ambiguous, turn.llm_routed,
// turn.deterministic_hit / miss, turn.turncache_hit, turn.offpath_routed,
// turn.cancelled). These events all carry a session_id attr pinned on
// the TurnLogger so a routing chip in one TUI program never sees a
// neighbouring session's events.
//
// This file plumbs those events into the Bubbletea program in the same
// shape observer.go uses for OnBackgroundTurn: a slog.Handler captures
// the records, filters by sid + routing-msg-prefix, builds the matching
// RoutingTier*Msg, and dispatches via tea.Program.Send (the only
// documented cross-goroutine entry point on *tea.Program).
//
// Lifecycle: [InstallRoutingObserver] returns a *RoutingObserver that
// the caller wires into their slog logger as one handler in a multi-
// handler set. [RoutingObserver.Attach] sets the *tea.Program and starts
// dispatching; [Detach] stops it.
//
// The observer also maintains a small in-memory ring of routing
// records (the last N events grouped by turn number) so the ctrl+r
// route-trace overlay (§8.3) can pretty-print "everything we know
// about turn N's routing pipeline" without re-reading the trace file.
package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
	"kitsoki/internal/trace"
)

// routingRingCap caps the number of *recent turns* the observer
// remembers routing events for; older turns roll off. Sized at 32
// because the ctrl+r overlay only ever inspects the focused turn —
// the buffer just needs to be deep enough that a user scrolling
// back through a long session still sees recent context.
const routingRingCap = 32

// Sender is the minimal interface RoutingObserver needs to dispatch
// tea.Msg values to a Bubbletea program. *tea.Program satisfies it
// directly (its Send method matches the signature) so production
// wiring is unchanged; tests can substitute a fake Sender to drive
// blocking-behaviour assertions without spinning up a real program.
type Sender interface {
	Send(msg tea.Msg)
}

// RoutingObserver subscribes to routing-tier slog events and forwards
// them to a bound [Sender] (in production, a *tea.Program). It
// satisfies slog.Handler so callers can plug it into a multi-handler
// logger.
//
// Construct with [NewRoutingObserver], wire into the logger handed to
// the orchestrator, then call [Attach] once the program is built.
// Detach() stops dispatch (the slog handler keeps running but no
// messages are pushed to the program).
//
// Dispatch is fire-and-forget: [Handle] launches one goroutine per
// matched event to call sender.Send. *tea.Program.Send blocks on a
// bounded internal msg channel, and the orchestrator's slog emission
// path may be holding a per-session lock — synchronous Send would
// backpressure into the lock-holder if the TUI render were slow.
// Routing events fire at human cadence (one or two per turn), so the
// extra goroutine cost is negligible.
type RoutingObserver struct {
	// sid filters events; an empty SessionID means "accept any
	// session", which is useful for headless tests that don't
	// pre-allocate session IDs.
	sid app.SessionID

	// mu guards sender + ring. Held briefly on Handle and on
	// Attach/Detach.
	mu sync.Mutex

	// sender is the dispatch target; nil before Attach is called or
	// after Detach. Production callers attach a *tea.Program; tests
	// may attach a fake Sender.
	sender Sender

	// ring holds per-turn routing event traces. Keyed by turn number;
	// the oldest turn entry is evicted when len(ring) > routingRingCap.
	ring map[int64]*RoutingTrace
	// order tracks turn numbers in insertion order for ring eviction.
	order []int64
}

// NewRoutingObserver constructs an observer that filters events to
// the supplied session ID. Pass an empty SessionID to accept every
// session (useful in tests).
func NewRoutingObserver(sid app.SessionID) *RoutingObserver {
	return &RoutingObserver{
		sid:  sid,
		ring: make(map[int64]*RoutingTrace),
	}
}

// Attach binds the observer to a *tea.Program. After Attach,
// matching routing slog events fan out as RoutingTier*Msg values via
// prog.Send (on a fire-and-forget goroutine — see the type doc on
// why). Safe to call from any goroutine.
//
// Passing nil is a no-op (useful from headless tests that want the
// ring buffer but no fanout).
func (o *RoutingObserver) Attach(prog *tea.Program) {
	o.attachSender(prog)
}

// AttachSender is the interface-typed counterpart to [Attach] —
// production callers should use Attach with a real *tea.Program;
// tests use AttachSender to inject a fake [Sender] (e.g. one that
// blocks forever, to prove Handle is non-blocking).
func (o *RoutingObserver) AttachSender(s Sender) {
	o.attachSender(s)
}

// attachSender normalises the typed-nil edge case: a typed-nil
// *tea.Program passed through the Sender interface would compare
// != nil under the interface comparison, so we unwrap it.
func (o *RoutingObserver) attachSender(s Sender) {
	o.mu.Lock()
	defer o.mu.Unlock()
	// Special-case a typed-nil *tea.Program: callers may pass nil
	// from headless paths and we want sender == nil after.
	if p, ok := s.(*tea.Program); ok && p == nil {
		o.sender = nil
		return
	}
	o.sender = s
}

// Detach clears the bound program; subsequent slog events still
// land in the ring buffer but no tea.Msg dispatch happens.
func (o *RoutingObserver) Detach() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sender = nil
}

// Trace returns a copy of the routing trace for turnNum (or nil when
// no events have been recorded for that turn yet). The returned
// slice is a snapshot — callers may iterate it without holding any
// lock.
func (o *RoutingObserver) Trace(turnNum int64) *RoutingTrace {
	o.mu.Lock()
	defer o.mu.Unlock()
	rt, ok := o.ring[turnNum]
	if !ok {
		return nil
	}
	// Defensive copy so callers can iterate without racing further
	// Handle() appends.
	out := &RoutingTrace{
		Turn:   rt.Turn,
		Events: make([]RoutingTraceEvent, len(rt.Events)),
	}
	copy(out.Events, rt.Events)
	return out
}

// LatestTurn returns the highest turn number the observer has seen
// routing events for, or 0 when the ring is empty. The TUI uses this
// to default the ctrl+r overlay to "the current turn."
func (o *RoutingObserver) LatestTurn() int64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.order) == 0 {
		return 0
	}
	return o.order[len(o.order)-1]
}

// RoutingTrace is the per-turn pretty-printed record the ctrl+r
// overlay renders. The events list is append-only in slog emission
// order.
type RoutingTrace struct {
	// Turn is the orchestrator turn number these events belong to.
	Turn int64
	// Events is the chronological list of routing-tier records.
	Events []RoutingTraceEvent
}

// RoutingTraceEvent is one slog routing record captured for the
// ctrl+r overlay. Attrs is a small map of the most-interesting fields
// (intent, confidence, model, etc.); the raw slog record isn't kept
// because slog.Record values can't be safely retained across
// Handle calls.
type RoutingTraceEvent struct {
	Time  time.Time
	Msg   string
	Attrs map[string]any
}

// ─── slog.Handler implementation ─────────────────────────────────────────────

// Enabled returns true for any level — the observer is interested in
// debug-level routing events, so it must accept them.
func (o *RoutingObserver) Enabled(_ context.Context, _ slog.Level) bool {
	return true
}

// Handle is the slog dispatch entry point. We filter on:
//   - msg starts with one of the routing-tier prefixes;
//   - the record's session_id attr matches o.sid (when o.sid != "").
//
// On match we append to the per-turn ring AND, if a program is
// attached, send the matching tea.Msg.
func (o *RoutingObserver) Handle(_ context.Context, r slog.Record) error {
	if !isRoutingMsg(r.Message) {
		return nil
	}

	attrs := make(map[string]any, 6)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})

	if o.sid != "" {
		if got, _ := attrs["session_id"].(string); got != "" && got != string(o.sid) {
			// Different session — ignore.
			return nil
		}
	}

	turnNum := attrInt64(attrs, "turn")
	o.recordEvent(turnNum, r.Time, r.Message, attrs)

	msg, ok := translateToTeaMsg(r.Message, attrs)
	if !ok {
		return nil
	}

	o.mu.Lock()
	sender := o.sender
	o.mu.Unlock()
	if sender != nil {
		// Fire-and-forget: tea.Program.Send blocks on a bounded
		// internal msg channel. The orchestrator may emit this slog
		// record while holding a per-session lock; a synchronous Send
		// would backpressure into the lock-holder if the TUI is slow
		// to drain. One goroutine per routing event is cheap (events
		// fire at human cadence, one or two per turn).
		go sender.Send(msg)
	}
	return nil
}

// WithAttrs returns a clone that shares the same ring; the new attrs
// are merged into every subsequent emission's attr map (slog
// guarantees the inner handler sees them).
//
// We don't need to maintain a separate inner handler because Handle
// reads attrs off the record directly — but for protocol correctness
// we hold the pre-attached attrs ourselves and merge them in.
func (o *RoutingObserver) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return o
	}
	clone := &routingObserverClone{base: o, pre: attrs}
	return clone
}

// WithGroup is a no-op for routing observation — we don't interpret
// nested groups.
func (o *RoutingObserver) WithGroup(_ string) slog.Handler { return o }

// routingObserverClone is the WithAttrs result. It captures the
// pre-attached attrs and merges them into the record's own attrs on
// each Handle call.
type routingObserverClone struct {
	base *RoutingObserver
	pre  []slog.Attr
}

func (c *routingObserverClone) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (c *routingObserverClone) Handle(ctx context.Context, r slog.Record) error {
	// Merge pre-attached attrs onto the record so the base handler
	// sees them in attrs map.
	r2 := r.Clone()
	r2.AddAttrs(c.pre...)
	return c.base.Handle(ctx, r2)
}

func (c *routingObserverClone) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(c.pre)+len(attrs))
	merged = append(merged, c.pre...)
	merged = append(merged, attrs...)
	return &routingObserverClone{base: c.base, pre: merged}
}

func (c *routingObserverClone) WithGroup(_ string) slog.Handler { return c }

// ─── translation: slog record → tea.Msg ─────────────────────────────────────

// translateToTeaMsg maps a routing-tier slog record to the matching
// RoutingTier*Msg. Returns (msg, true) on a known mapping, (nil, false)
// when the message should populate the ring but not fan out (e.g.
// turn.semantic_miss — the chip doesn't need a per-miss notice; the
// final Hit message includes the implicit miss trail).
func translateToTeaMsg(msg string, attrs map[string]any) (tea.Msg, bool) {
	switch msg {
	case trace.EvTurnDeterministicHit:
		return RoutingTierHitMsg{
			Tier:   TierDeterministic,
			Intent: attrString(attrs, "intent"),
			Reason: attrString(attrs, "match_type"),
		}, true

	case trace.EvTurnDeterministicMiss:
		return RoutingTierMissMsg{Tier: TierDeterministic}, true

	case trace.EvTurnSemanticHit:
		tier := TierSemantic
		if reason := attrString(attrs, "reason"); strings.HasPrefix(reason, "template:") {
			tier = TierTemplate
		}
		return RoutingTierHitMsg{
			Tier:       tier,
			Intent:     attrString(attrs, "intent"),
			Reason:     attrString(attrs, "reason"),
			Confidence: attrFloat(attrs, "confidence"),
		}, true

	case trace.EvTurnSemanticMiss:
		return RoutingTierMissMsg{Tier: TierSemantic}, true

	case trace.EvTurnSemanticAmbiguous:
		cands := attrStrings(attrs, "candidates")
		return RoutingAmbiguousMsg{Candidates: cands}, true

	case trace.EvTurnTurncacheHit:
		return RoutingTierHitMsg{
			Tier:       TierTurncache,
			Intent:     attrString(attrs, "intent"),
			Reason:     "cache",
			Confidence: attrFloat(attrs, "confidence"),
			Hits:       int(attrInt64(attrs, "hits")),
		}, true

	case trace.EvTurnLLMRouted:
		model := attrString(attrs, "model")
		if model == "" {
			model = "llm"
		}
		return RoutingTierHitMsg{
			Tier:       TierLLM,
			Intent:     attrString(attrs, "intent"),
			Reason:     model,
			Confidence: attrFloat(attrs, "confidence"),
			Latency:    attrDuration(attrs, "dur"),
		}, true

	case trace.EvTurnOffpathRouted:
		return RoutingTierHitMsg{
			Tier:   TierOffpath,
			Intent: attrString(attrs, "intent"),
			Reason: attrString(attrs, "reason"),
		}, true

	case trace.EvTurnCancelled:
		return RoutingCancelMsg{}, true
	}
	return nil, false
}

// isRoutingMsg returns true for any slog msg the observer cares about.
// Centralised so the filter and the translator agree.
func isRoutingMsg(msg string) bool {
	switch msg {
	case trace.EvTurnDeterministicHit,
		trace.EvTurnDeterministicMiss,
		trace.EvTurnSemanticHit,
		trace.EvTurnSemanticMiss,
		trace.EvTurnSemanticAmbiguous,
		trace.EvTurnTurncacheHit,
		trace.EvTurnLLMRouted,
		trace.EvTurnOffpathRouted,
		trace.EvTurnCancelled:
		return true
	}
	return false
}

// recordEvent appends a record to the per-turn ring, evicting the
// oldest turn when the ring is full.
func (o *RoutingObserver) recordEvent(turnNum int64, ts time.Time, msg string, attrs map[string]any) {
	if turnNum <= 0 {
		// Some events (e.g. EvTurnCancelled at orchestrator shutdown)
		// may not carry a turn number; still record under turn=0 so
		// the overlay can show "session-scope" entries.
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	rt, ok := o.ring[turnNum]
	if !ok {
		rt = &RoutingTrace{Turn: turnNum}
		o.ring[turnNum] = rt
		o.order = append(o.order, turnNum)
		// Evict when the order ring overflows.
		if len(o.order) > routingRingCap {
			drop := o.order[0]
			o.order = o.order[1:]
			delete(o.ring, drop)
		}
	}
	// Defensive copy of the attrs map so subsequent Handle calls (which
	// reuse the input map) don't mutate our captured event.
	cp := make(map[string]any, len(attrs))
	for k, v := range attrs {
		cp[k] = v
	}
	rt.Events = append(rt.Events, RoutingTraceEvent{Time: ts, Msg: msg, Attrs: cp})
}

// ─── attr helpers ────────────────────────────────────────────────────────────

func attrString(m map[string]any, k string) string {
	if v, ok := m[k]; ok {
		switch s := v.(type) {
		case string:
			return s
		case fmt.Stringer:
			return s.String()
		}
	}
	return ""
}

func attrInt64(m map[string]any, k string) int64 {
	if v, ok := m[k]; ok {
		switch n := v.(type) {
		case int64:
			return n
		case int:
			return int64(n)
		case float64:
			return int64(n)
		}
	}
	return 0
}

func attrFloat(m map[string]any, k string) float64 {
	if v, ok := m[k]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case float32:
			return float64(n)
		case int64:
			return float64(n)
		case int:
			return float64(n)
		}
	}
	return 0
}

func attrDuration(m map[string]any, k string) time.Duration {
	if v, ok := m[k]; ok {
		switch d := v.(type) {
		case time.Duration:
			return d
		case int64:
			return time.Duration(d)
		case float64:
			return time.Duration(d)
		}
	}
	return 0
}

func attrStrings(m map[string]any, k string) []string {
	v, ok := m[k]
	if !ok {
		return nil
	}
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, e := range s {
			if str, ok := e.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

// ─── ctrl+r overlay rendering ────────────────────────────────────────────────

// FormatRoutingTrace pretty-prints the supplied trace for the ctrl+r
// overlay. The format is a compact "one event per line" table tuned for
// debugging:
//
//	turn 3 routing trace
//	  12:01:02.345  turn.deterministic_miss   state=foyer
//	  12:01:02.346  turn.semantic_hit         intent=ford  reason=synonym:wade  confidence=0.90
//	  12:01:02.347  turn.llm_routed           model=claude-haiku  dur=2.4s  confidence=0.81
//
// Returns "" when trace is nil or has no events.
func FormatRoutingTrace(t *RoutingTrace) string {
	if t == nil || len(t.Events) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "turn %d routing trace\n", t.Turn)
	for _, ev := range t.Events {
		fmt.Fprintf(&sb, "  %s  %s", ev.Time.Format("15:04:05.000"), padRightRouting(ev.Msg, 26))
		// Print a deterministic subset of interesting attrs.
		for _, k := range []string{"intent", "reason", "confidence", "candidates", "model", "dur", "hits", "match_type"} {
			if v, ok := ev.Attrs[k]; ok {
				fmt.Fprintf(&sb, "  %s=%v", k, v)
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// padRightRouting returns s padded with spaces to width n. Used to
// align the message column in the routing-trace overlay. Named to
// avoid colliding with the transcript model's existing padRight.
func padRightRouting(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
