// Runnable godoc examples for the [proposal] surface. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/proposal/...`.
package proposal_test

import (
	"context"
	"fmt"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/proposal"
)

// ExampleProposal_lifecycle is the canonical draft-lifecycle worked example:
// New → two SetDrafts (the second carrying refine feedback) → Transition →
// ToMap round-trip, the same trace shown in the package doc.
func ExampleProposal_lifecycle() {
	p := proposal.New("p-1", "shell_command", "sess-1")
	fmt.Println("status:  ", p.Status)

	p.SetDraft(map[string]any{"cmd": "echo hi"}, "")
	p.SetDraft(map[string]any{"cmd": "echo bye"}, "say bye instead")

	fmt.Println("versions:", len(p.History))
	fmt.Println("feedback:", p.History[0].Feedback) // lands on the prior version
	fmt.Println("current: ", p.Current["cmd"])

	p.Transition(proposal.StatusReviewing)

	// Round-trip through the world-state map form.
	back := proposal.FromMap(p.ToMap())
	fmt.Println("restored:", back.Status, back.Current["cmd"])
	// Output:
	// status:   drafting
	// versions: 2
	// feedback: say bye instead
	// current:  echo bye
	// restored: reviewing echo bye
}

// ExampleExecute shows a minimal synchronous execution: a kind whose execute
// block invokes a host handler with a {{p.cmd}} arg drawn from the draft,
// landing the proposal in StatusDone with an OK result.
func ExampleExecute() {
	reg := host.NewRegistry()
	reg.Register("host.echo", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"out": args["cmd"]}}, nil
	})

	kind := &app.ProposalKind{
		Execute: &app.ProposalExecute{
			Invoke: "host.echo",
			With:   map[string]any{"cmd": "{{p.cmd}}"},
		},
	}

	p := proposal.New("p-2", "shell_command", "sess-1")
	p.Current = map[string]any{"cmd": "echo hello"}

	res := proposal.Execute(context.Background(), p, kind, reg)
	fmt.Println("err:    ", res.Err)
	fmt.Println("status: ", res.Proposal.Status)
	fmt.Println("ok:     ", res.Proposal.Result.OK)
	fmt.Println("out:    ", res.HostResult.Data["out"])
	// Output:
	// err:     <nil>
	// status:  done
	// ok:      true
	// out:     echo hello
}
