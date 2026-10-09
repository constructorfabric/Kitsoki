// Runnable godoc examples for the agents registry. Each Example
// function's // Output: block is checked by
// `go test -run "^Example" ./internal/agents/...`.
package agents_test

import (
	"fmt"

	"kitsoki/internal/agents"
)

// ExampleBuildRegistry is the worked example from the package doc: an app
// override REPLACES the matching builtin by name, while the other builtins
// stay resolvable. It mirrors the trace in the package comment.
func ExampleBuildRegistry() {
	specs := []agents.BuildSpec{{
		Name:         agents.NameDefaultAgent, // overrides the builtin
		SystemPrompt: "You are the river guide.",
		Tools:        []string{"Read"},
	}}
	reg, err := agents.BuildRegistry(specs)
	if err != nil {
		panic(err)
	}

	agent, ok := reg.Get(agents.NameDefaultAgent)
	fmt.Println("override present:", ok)
	fmt.Println("override prompt: ", agent.SystemPrompt)

	_, ok = reg.Get(agents.NameStoryAuthor)
	fmt.Println("builtin present: ", ok)

	_, ok = reg.Get("no-such-agent")
	fmt.Println("unknown present: ", ok)
	// Output:
	// override present: true
	// override prompt:  You are the river guide.
	// builtin present:  true
	// unknown present:  false
}

// ExampleBuiltinNames shows the names the loader cross-references, in
// NewBuiltins registration order.
func ExampleBuiltinNames() {
	for _, name := range agents.BuiltinNames() {
		fmt.Println(name)
	}
	// Output:
	// default-agent
	// story-author
	// kitsoki-engineer
	// story-improver
	// kitsoki-improver
	// story-bug-reporter
	// kitsoki-bug-reporter
	// story-explainer
	// kitsoki-explainer
}
