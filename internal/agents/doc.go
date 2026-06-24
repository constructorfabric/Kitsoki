// Package agents is the registry of (system prompt, model, tool surface,
// optional cwd) tuples the meta-mode controller hands to the harness. It
// sits between the app loader and [kitsoki/internal/metamode]: the loader
// turns YAML agents: blocks into [BuildSpec]s, [BuildRegistry] folds those
// over the baked-in builtins, and the controller resolves a turn's agent
// by name via [Registry.Get] before dispatching to the LLM harness.
//
// An [Agent] is pure configuration — no behaviour, no I/O. The package
// owns one decision: which named bundle of prompt + model + allowed tools
// + cwd a given meta-mode chat runs under. Everything downstream (running
// the subprocess, gating tools, committing edits) belongs to the harness
// and the controller, not here.
//
// # Algorithm
//
// Registration is last-writer-wins on the agent name:
//
//  1. [NewBuiltins] constructs an empty registry and Registers the seven
//     builtin agents (see the Name* constants) in a fixed order.
//  2. [BuildRegistry] starts from NewBuiltins, then Registers each
//     [BuildSpec] the loader supplies. A spec whose Name equals a builtin
//     REPLACES that builtin for the lifetime of the returned registry;
//     a new name is simply added. This is how an app's agents: block
//     overrides, say, default-agent without forking the package.
//  3. [Registry.Get] is the runtime lookup the controller uses each turn;
//     it returns the Agent and an ok flag, never an error.
//
// # Invariants
//
//   - Every builtin's Agent.Name equals the matching Name* constant, and
//     [BuiltinNames] lists exactly those constants in registration order.
//     The constants exist so the name a builder stamps and the name the
//     loader cross-references can never drift apart.
//   - Register overwrites on name collision — callers (tests, app
//     overrides) rely on that rather than getting a duplicate error.
//   - Tool lists are informational at this layer. Every claude subprocess
//     currently runs with --permission-mode bypassPermissions, so an
//     Agent.Tools entry documents intent for prompt authors and reviewers
//     rather than acting as a runtime gate (see Non-goals).
//
// # Worked example
//
// Building a registry from the builtins plus one app override, then
// resolving an agent by name:
//
//	specs := []agents.BuildSpec{{
//	    Name:         "default-agent",   // overrides the builtin
//	    SystemPrompt: "You are the river guide.",
//	    Tools:        []string{"Read"},
//	}}
//	reg, _ := agents.BuildRegistry(specs)
//
//	a, ok := reg.Get("default-agent")
//	// ok == true
//	// a.SystemPrompt == "You are the river guide."  (override won)
//	// a.Tools        == ["Read"]
//
//	_, ok = reg.Get("story-author")
//	// ok == true — untouched builtin still present
//
//	_, ok = reg.Get("no-such-agent")
//	// ok == false
//
// A runnable form of this trace lives in [ExampleBuildRegistry].
//
// # Lifecycle
//
// Compile-time: the builtin prompts are //go:embed-ed .md files, so the
// system-prompt text is fixed into the binary. Load-time: [BuildRegistry]
// runs once per app load, after the loader has resolved every BuildSpec to
// its final form (literal prompt text, fully-qualified tool names,
// env-expanded cwd). Runtime: the controller calls [Registry.Get] per
// meta-mode turn. A registry value built at load time is read concurrently
// at runtime — see the concurrency note on [Registry].
//
// # Non-goals
//
//   - No dynamic agent-definition syntax. Specs arrive fully resolved from
//     the app loader; this package never parses YAML, reads files, or
//     expands env vars — keeping file-path and config concerns one-way out
//     of the registry.
//   - No in-process agent isolation. Every agent runs in the same Go
//     process and, today, the same bypassPermissions subprocess; an
//     Agent is configuration, not a sandbox.
//   - No per-agent tool scoping enforced here. Agent.Tools records the
//     intended surface; actually restricting it (e.g. via
//     --allowed-tools) is the harness's responsibility, so the same
//     names pass straight through if a future iteration wires real
//     gating.
//   - No agent behaviour. Running the turn, committing edits, and
//     surfacing errors live in [kitsoki/internal/metamode] and the host
//     layer, not here.
//
// # Reference
//
// The meta-mode chat surface, the verb grid (edit/ask/bug), and how each
// builtin is reached are documented in docs/stories/meta-mode.md. The bug
// pile format the *-bug-reporter agents write through `kitsoki bug create`
// is documented in docs/stories/bugs.md.
package agents
