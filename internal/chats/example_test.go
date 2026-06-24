// Runnable godoc examples for the [Store] surface. Each Example function's
// // Output: block is checked by
// `go test -run "^Example" ./internal/chats/...`.
package chats_test

import (
	"context"
	"errors"
	"fmt"

	"kitsoki/internal/chats"
	"kitsoki/internal/store"
)

// ExampleStore is the canonical worked example from the package doc: open a
// store, resolve a chat (get-or-create), append two turns, read the
// transcript back, then resolve the same tuple again and observe reuse.
func ExampleStore() {
	s, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()

	cs, err := chats.NewStore(s.DB())
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	// First Resolve creates the chat.
	c, created, err := cs.Resolve(ctx, "bugfix", "phase_3", "PROJ-123", "triage")
	if err != nil {
		panic(err)
	}
	fmt.Println("created:", created)
	fmt.Println("status: ", c.Status)

	// Append two turns; seq is dense and monotonic from 0.
	m1, err := cs.AppendMessage(ctx, c.ID, "user", "what changed?", nil)
	if err != nil {
		panic(err)
	}
	m2, err := cs.AppendMessage(ctx, c.ID, "assistant", "the lockfile", nil)
	if err != nil {
		panic(err)
	}
	fmt.Println("seq1:   ", m1.Seq)
	fmt.Println("seq2:   ", m2.Seq)

	// Read the transcript back.
	msgs, err := cs.Transcript(ctx, c.ID, 0)
	if err != nil {
		panic(err)
	}
	fmt.Println("turns:  ", len(msgs))

	// Resolving the same tuple returns the existing chat (no new row).
	again, created2, err := cs.Resolve(ctx, "bugfix", "phase_3", "PROJ-123", "triage")
	if err != nil {
		panic(err)
	}
	fmt.Println("created:", created2)
	fmt.Println("same:   ", again.ID == c.ID)

	// Output:
	// created: true
	// status:  active
	// seq1:    0
	// seq2:    1
	// turns:   2
	// created: false
	// same:    true
}

// ExampleStore_queue shows the drive queue: enqueue a turn, dequeue it
// (pending → dispatching), then mark it done.
func ExampleStore_queue() {
	s, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()

	cs, err := chats.NewStore(s.DB())
	if err != nil {
		panic(err)
	}
	ctx := context.Background()

	c, _, err := cs.Resolve(ctx, "bugfix", "phase_3", "", "queue demo")
	if err != nil {
		panic(err)
	}

	d, err := cs.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:    c.ID,
		Transport: chats.DriveTransportTUI,
		Payload:   "run the linter",
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("enqueued:", d.Status)

	claimed, err := cs.Dequeue(ctx, c.ID)
	if err != nil {
		panic(err)
	}
	fmt.Println("claimed: ", claimed.Status)

	// The assistant reply lands at seq 0; mark the drive done against it.
	if err := cs.MarkDriveDone(ctx, claimed.DriveID, 0); err != nil {
		panic(err)
	}
	final, err := cs.GetDrive(ctx, claimed.DriveID)
	if err != nil {
		panic(err)
	}
	fmt.Println("final:   ", final.Status)

	// A second dequeue finds nothing pending.
	_, err = cs.Dequeue(ctx, c.ID)
	fmt.Println("drained: ", errors.Is(err, chats.ErrNoPendingDrive))

	// Output:
	// enqueued: pending
	// claimed:  dispatching
	// final:    done
	// drained:  true
}
