package agents

import (
	"sort"
	"sync"
)

// Builtin agent names. These are the registry keys NewBuiltins seeds and
// the strings meta-mode verbs in docs/stories/meta-mode.md resolve to;
// they double as the public contract an app's agents: block overrides by
// re-declaring under the same name. They live as constants so the name a
// builder stamps onto its Agent.Name and the name BuiltinNames advertises
// can never drift apart.
const (
	// NameDefaultAgent is the toolless fallback agent used when a caller
	// names no agent.
	NameDefaultAgent = "default-agent"
	// NameStoryAuthor is the conversational story/YAML editor.
	NameStoryAuthor = "story-author"
	// NameKitsokiEngineer edits Go code in the kitsoki repo and runs tests.
	NameKitsokiEngineer = "kitsoki-engineer"
	// NameStoryImprover reviews a running story session for prompt, tool,
	// and flow-test improvements.
	NameStoryImprover = "story-improver"
	// NameKitsokiImprover reviews a running kitsoki session for engine-level
	// prompt, tool, and workflow improvements.
	NameKitsokiImprover = "kitsoki-improver"
	// NameStoryBugReporter files a bug against the running story.
	NameStoryBugReporter = "story-bug-reporter"
	// NameKitsokiBugReporter files a bug against kitsoki itself.
	NameKitsokiBugReporter = "kitsoki-bug-reporter"
	// NameStoryExplainer is the read-only Q&A sibling of story-author.
	NameStoryExplainer = "story-explainer"
	// NameKitsokiExplainer is the read-only Q&A sibling of kitsoki-engineer.
	NameKitsokiExplainer = "kitsoki-explainer"
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
//
// The implementation returned by [NewBuiltins] / [BuildRegistry] is safe
// for concurrent use: the typical pattern is a single load-time burst of
// Register calls followed by read-only Get/List at runtime, but the
// internal lock makes interleaved Registers safe too. Get and List never
// error — a missing name surfaces as Get's ok=false, and List returns the
// names in sorted order (distinct from [BuiltinNames], which preserves
// registration order).
type Registry interface {
	Get(name string) (Agent, bool)
	List() []string
	Register(a Agent)
}

// NewBuiltins returns a Registry pre-populated with the agents that
// ship with kitsoki today.
func NewBuiltins() Registry {
	r := &registry{agents: make(map[string]Agent)}
	r.Register(defaultAgent())
	r.Register(storyAuthor())
	r.Register(kitsokiEngineer())
	r.Register(storyImprover())
	r.Register(kitsokiImprover())
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
		NameDefaultAgent,
		NameStoryAuthor,
		NameKitsokiEngineer,
		NameStoryImprover,
		NameKitsokiImprover,
		NameStoryBugReporter,
		NameKitsokiBugReporter,
		NameStoryExplainer,
		NameKitsokiExplainer,
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
