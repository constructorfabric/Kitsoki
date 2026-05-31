// Runnable godoc examples for the [jobs.Scheduler] surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/jobs/...`.
package jobs_test

import (
	"context"
	"fmt"

	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

// ExampleScheduler_Submit is the canonical worked example: submit a handler
// that echoes a value, subscribe, and read the terminal JobEvent. This mirrors
// the trace in the package doc.
func ExampleScheduler_Submit() {
	sched := jobs.NewInMemoryScheduler()

	handler := func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"output": "hello"}}, nil
	}

	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind:    "demo",
		Handler: handler,
	})
	if err != nil {
		panic(err)
	}

	ch, unsub := sched.Subscribe(id)
	defer unsub()

	ev := <-ch // terminal JobEvent: the channel closes after this send.
	fmt.Println("status:", ev.Status)
	fmt.Println("output:", ev.Result.Data["output"])
	// Output:
	// status: done
	// output: hello
}

// ExampleScheduler_Submit_failure shows the terminal classification for a
// handler whose host.Result carries a non-empty Error: the job lands in
// JobFailed with that string surfaced on the event, not JobDone.
func ExampleScheduler_Submit_failure() {
	sched := jobs.NewInMemoryScheduler()

	handler := func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Error: "domain error"}, nil
	}

	id, err := sched.Submit(context.Background(), jobs.JobSpec{
		Kind:    "demo",
		Handler: handler,
	})
	if err != nil {
		panic(err)
	}

	ch, unsub := sched.Subscribe(id)
	defer unsub()

	ev := <-ch
	fmt.Println("status:", ev.Status)
	fmt.Println("error: ", ev.Error)
	// Output:
	// status: failed
	// error:  domain error
}
