// Runnable godoc examples for the runstatus snapshot surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/runstatus/...`.
package runstatus_test

import (
	"encoding/json"
	"fmt"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/store"
)

// ExampleFromHistory is the canonical worked example from the package doc: a
// two-event history (a StateEntered, then a HarnessError) becomes a Snapshot
// whose SessionHeader is derived from the events and whose Events carry the
// promoted level/msg plus the decoded payload in Attrs.
func ExampleFromHistory() {
	def := &app.AppDef{App: app.AppMeta{ID: "demo", Version: "0.0.1"}}

	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	hist := store.History{
		{Turn: 1, Seq: 0, Ts: base, Kind: store.StateEntered, StatePath: "greet",
			Payload: json.RawMessage(`{"state":"greet"}`)},
		{Turn: 1, Seq: 1, Ts: base.Add(time.Millisecond), Kind: store.HarnessError,
			Payload: json.RawMessage(`{"detail":"boom"}`)},
	}

	snap, err := runstatus.FromHistory(hist, def, "s1")
	if err != nil {
		panic(err)
	}

	fmt.Println("session:    ", snap.Session.SessionID, snap.Session.AppID)
	fmt.Println("state/turn: ", snap.Session.CurrentState, snap.Session.Turn)
	fmt.Println("terminal:   ", snap.Session.Terminal)
	for _, ev := range snap.Events {
		fmt.Printf("event:       %-5s %s\n", ev.Level, ev.Msg)
	}
	// Output:
	// session:     s1 demo
	// state/turn:  greet 1
	// terminal:    false
	// event:       INFO  machine.state_entered
	// event:       ERROR harness.error
}

// ExampleTraceEvent_UnmarshalJSON shows the decode contract: well-known slog
// keys are promoted to typed fields and every other key lands in Attrs, so an
// event-specific payload like "intent" survives without a schema change.
func ExampleTraceEvent_UnmarshalJSON() {
	line := `{"time":"2026-05-31T12:00:00Z","level":"INFO","msg":"machine.transition",` +
		`"turn":3,"state_path":"bar.lit","intent":"order_drink"}`

	var ev runstatus.TraceEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		panic(err)
	}

	fmt.Println("msg:      ", ev.Msg)
	fmt.Println("turn:     ", ev.Turn)
	fmt.Println("statePath:", ev.StatePath)
	fmt.Println("attr:     ", ev.Attrs["intent"])
	// Output:
	// msg:       machine.transition
	// turn:      3
	// statePath: bar.lit
	// attr:      order_drink
}
