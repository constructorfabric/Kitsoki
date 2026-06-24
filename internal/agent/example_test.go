// Runnable godoc examples for the agent plugin contract. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/agent/...`.
package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"kitsoki/internal/agent"
)

// ExampleNew is the canonical in-process worked example: wrap a deterministic
// AskFunc as an Agent, ask it under a schema, and validate the submission —
// the same happy-path trace shown in the package doc.
func ExampleNew() {
	// A stub agent that always submits a fixed decision.
	o := agent.New(func(_ context.Context, req agent.AskRequest) (agent.AskResponse, error) {
		_ = req // a real plugin would reason over req.PromptText
		return agent.AskResponse{
			Submission: json.RawMessage(`{"decision":"go"}`),
		}, nil
	})
	defer o.Close()

	schema := json.RawMessage(`{
		"type": "object",
		"required": ["decision"],
		"properties": {"decision": {"type": "string"}}
	}`)

	resp, err := o.Ask(context.Background(), agent.AskRequest{
		Verb:       "decide",
		PromptText: "ship it?",
		SchemaJSON: schema,
	})
	if err != nil {
		panic(err)
	}

	// Kitsoki is the validation authority: the submission is checked against
	// the request schema before binding.
	if verr := agent.ValidateSubmission(schema, resp.Submission); verr != nil {
		panic(verr)
	}

	fmt.Printf("submission: %s\n", resp.Submission)
	fmt.Println("valid:      true")
	// Output:
	// submission: {"decision":"go"}
	// valid:      true
}

// ExampleValidateSubmission_schemaInvalid shows the rejection path: a
// submission missing a required property yields an *agent.AskError with
// Kind "schema_invalid".
func ExampleValidateSubmission_schemaInvalid() {
	schema := json.RawMessage(`{
		"type": "object",
		"required": ["decision"]
	}`)
	submission := json.RawMessage(`{"reason":"unsure"}`)

	err := agent.ValidateSubmission(schema, submission)

	var ae *agent.AskError
	if errors.As(err, &ae) {
		fmt.Println("kind:", ae.Kind)
	}
	// Output:
	// kind: schema_invalid
}

// ExampleBuildRegistry shows registry construction from plugin declarations
// and alias resolution, including the default-agent fallback for an unknown
// alias.
func ExampleBuildRegistry() {
	// A stub in-process agent injected under the default alias.
	stub := agent.New(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{Submission: json.RawMessage(`{}`)}, nil
	})

	// BuildRegistry with no declarations and no harness yields an empty
	// registry; the stub is then injected programmatically.
	reg, err := agent.BuildRegistry(map[string]*agent.PluginDecl{}, nil)
	if err != nil {
		panic(err)
	}
	reg.Register(agent.DefaultAgentName, stub)
	defer reg.Close()

	// An explicit alias resolves to itself; an unknown alias falls back to the
	// default rather than erroring.
	def, errDefault := reg.Resolve(agent.DefaultAgentName)
	fallback, errUnknown := reg.Resolve("agent.unknown")

	fmt.Println("default name:", agent.DefaultAgentName)
	fmt.Println("resolved default:", def != nil && errDefault == nil)
	fmt.Println("unknown falls back (no error):", fallback != nil && errUnknown == nil)
	// Output:
	// default name: agent.claude
	// resolved default: true
	// unknown falls back (no error): true
}
