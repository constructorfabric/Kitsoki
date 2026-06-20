// Runnable godoc examples for the [chathost] adapter surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/chathost/...`.
package chathost_test

import (
	"context"
	"errors"
	"fmt"

	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// ExampleNewAdapter is the canonical worked example from the package doc:
// wrap a concrete chats.Store as a host.ChatStore, then drive one
// Create→Get round-trip through the host interface and observe that the
// record crosses the boundary unchanged.
func ExampleNewAdapter() {
	s, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()

	cs, err := chats.NewStore(s.DB())
	if err != nil {
		panic(err)
	}

	// The adapter is a host.ChatStore; callers never touch chats again.
	var a host.ChatStore = chathost.NewAdapter(cs)
	ctx := context.Background()

	rec, err := a.Create(ctx, "my-app", "agent", "", "Test Chat")
	if err != nil {
		panic(err)
	}
	got, err := a.Get(ctx, rec.ID)
	if err != nil {
		panic(err)
	}

	fmt.Println("title:   ", got.Title)
	fmt.Println("status:  ", got.Status)
	fmt.Println("same id: ", got.ID == rec.ID)
	// Output:
	// title:    Test Chat
	// status:   active
	// same id:  true
}

// ExampleNewAdapter_sentinelTranslation shows the error-translation half of
// the adapter's job: a chats-package sentinel surfaces through the host
// interface as the matching host sentinel, so callers branch on host
// errors without importing chats. Dequeue on an empty queue is the
// simplest case.
func ExampleNewAdapter_sentinelTranslation() {
	s, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	defer func() { _ = s.Close() }()

	cs, err := chats.NewStore(s.DB())
	if err != nil {
		panic(err)
	}

	a := chathost.NewAdapter(cs)
	ctx := context.Background()

	c, err := a.Create(ctx, "my-app", "agent", "", "x")
	if err != nil {
		panic(err)
	}

	_, err = a.Dequeue(ctx, c.ID) // nothing enqueued
	fmt.Println("is host.ErrNoPendingDrive:", errors.Is(err, host.ErrNoPendingDrive))
	// Output:
	// is host.ErrNoPendingDrive: true
}
