// Package agents defines the (system prompt, model, tool surface,
// optional cwd) tuples the meta-mode controller hands to the harness,
// and a name-keyed registry of them. Builtins are baked in at process
// start; future phases register additional agents.
package agents

import (
	"sort"
	"sync"
)

// Agent bundles the configuration the harness needs to run an LLM
// turn on a meta-mode chat: the resolved system prompt, an optional
// model override, the canonical tool names the agent is allowed to
// call, and an optional cwd (already env-expanded by the registry).
type Agent struct {
	Name         string
	SystemPrompt string
	Model        string
	Tools        []string
	DefaultCwd   string
}

// Registry is a name → Agent lookup. Register overwrites on
// name-collision; tests and future phases rely on that.
type Registry interface {
	Get(name string) (Agent, bool)
	List() []string
	Register(a Agent)
}

// NewBuiltins returns a Registry pre-populated with the agents that
// ship with kitsoki today.
func NewBuiltins() Registry {
	r := &registry{agents: make(map[string]Agent)}
	r.Register(defaultOracle())
	r.Register(storyAuthor())
	r.Register(kitsokiEngineer())
	r.Register(storyBugReporter())
	r.Register(kitsokiBugReporter())
	r.Register(storyExplainer())
	r.Register(kitsokiExplainer())
	return r
}

// BuiltinNames returns the names of agents pre-registered by NewBuiltins().
// Used by the loader for cheap cross-reference validation without building
// a full registry. Order matches NewBuiltins() registration; callers that
// need a stable lexicographic order should sort the returned slice.
func BuiltinNames() []string {
	return []string{
		"default-oracle",
		"story-author",
		"kitsoki-engineer",
		"story-bug-reporter",
		"kitsoki-bug-reporter",
		"story-explainer",
		"kitsoki-explainer",
	}
}

type registry struct {
	mu     sync.RWMutex
	agents map[string]Agent
}

func (r *registry) Get(name string) (Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[name]
	return a, ok
}

func (r *registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (r *registry) Register(a Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.agents[a.Name] = a
}
