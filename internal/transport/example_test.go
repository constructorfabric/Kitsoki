// Runnable godoc examples for the transport surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/transport/...`.
package transport_test

import (
	"context"
	"fmt"

	"kitsoki/internal/transport"
)

// ExampleRegistry_Post is the in-process equivalent of the package's worked
// example: register the TUI driver, Post through the registry, and observe
// that the message was dispatched and buffered. The TUI driver is used (not
// Jira/Bitbucket) so the example needs no network and stays deterministic —
// the assigned message ID is an opaque ULID, so we assert on the buffered
// body rather than the ID.
func ExampleRegistry_Post() {
	tui := transport.NewTUITransport()

	r := transport.NewRegistry()
	r.Register(tui)

	id, err := r.Post(
		context.Background(),
		transport.SessionKey{Transport: "tui", Thread: "S-1"},
		transport.Message{Title: "Fix landed", Body: "Patched the off-by-one"},
	)
	if err != nil {
		panic(err)
	}

	// Routing an unknown transport key is the documented error path.
	_, err = r.Post(context.Background(), transport.SessionKey{Transport: "slack"}, transport.Message{})

	fmt.Println("posted:", id != "")
	fmt.Println("pending:", tui.Pending())
	fmt.Println("unknown err:", err != nil)
	// Output:
	// posted: true
	// pending: 1
	// unknown err: true
}

// ExampleTUITransport_Drain shows the buffer lifecycle: Post accumulates
// messages, Drain returns them all and resets the buffer to empty, and the
// registry fills in the default bot marker on the way through.
func ExampleTUITransport_Drain() {
	tui := transport.NewTUITransport()
	r := transport.NewRegistry()
	r.Register(tui)

	key := transport.SessionKey{Transport: "tui", Thread: "S-1"}
	_, _ = r.Post(context.Background(), key, transport.Message{Body: "first"})
	_, _ = r.Post(context.Background(), key, transport.Message{Body: "second"})

	drained := tui.Drain()
	fmt.Println("drained:", len(drained))
	fmt.Println("first body:", drained[0].Msg.Body)
	fmt.Println("first marker:", drained[0].Msg.BotMarker)
	fmt.Println("pending after drain:", tui.Pending())
	// Output:
	// drained: 2
	// first body: first
	// first marker: [kitsoki]
	// pending after drain: 0
}
