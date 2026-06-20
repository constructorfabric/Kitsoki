// registry.go holds the per-app agent plugin registry.
//
// A Registry maps plugin alias names (e.g. "agent.claude",
// "agent.autofix_fixer") to Agent implementations. It is constructed at
// session-construction time from the app's agent_plugins declarations (see
// BuildRegistryFromDef) and injected into the dispatch context.

package agent

import (
	"fmt"
	"sync"
)

// Registry holds the per-app agent plugins keyed by alias name
// (the name used in the `hosts:` YAML block, e.g. "agent.claude").
// It is safe for concurrent read from multiple goroutines; mutations
// happen only at construction time (single-threaded).
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Agent
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{plugins: make(map[string]Agent)}
}

// Register stores o under name. Panics on duplicate registration so
// misconfigured stories fail fast at boot rather than silently shadowing.
func (r *Registry) Register(name string, o Agent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[name]; exists {
		panic(fmt.Sprintf("agent registry: duplicate plugin %q", name))
	}
	r.plugins[name] = o
}

// Get returns the Agent registered under name, or (nil, false) when not found.
func (r *Registry) Get(name string) (Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.plugins[name]
	return o, ok
}

// Resolve returns the Agent for name, falling back to "agent.claude" when
// name is empty or when the specific alias is absent. Returns (nil, error)
// when neither the named alias nor the default is registered.
func (r *Registry) Resolve(name string) (Agent, error) {
	if name == "" {
		name = DefaultAgentName
	}
	r.mu.RLock()
	o, ok := r.plugins[name]
	r.mu.RUnlock()
	if ok {
		return o, nil
	}
	// Fall back to the default agent when the requested alias is absent.
	if name != DefaultAgentName {
		r.mu.RLock()
		o, ok = r.plugins[DefaultAgentName]
		r.mu.RUnlock()
		if ok {
			return o, nil
		}
	}
	return nil, fmt.Errorf("agent registry: no agent registered for %q (and no default %q fallback)", name, DefaultAgentName)
}

// IsLocalLLM reports whether the alias name resolves to a *LocalLLMAgent
// backend. The host dispatcher uses this to decide whether a schema_invalid
// validation rejection is eligible for the local-model → agent.claude
// fallback (step 4): only local-model backends fail soft, every other
// transport (external MCP plugins, the claude CLI itself) fails hard so a
// genuine schema regression is not silently papered over.
//
// Resolution mirrors Resolve: an empty or absent alias falls back to the
// default agent, which is never a local_llm, so the common case returns false.
func (r *Registry) IsLocalLLM(name string) bool {
	o, err := r.Resolve(name)
	if err != nil {
		return false
	}
	_, ok := o.(*LocalLLMAgent)
	return ok
}

// Close closes all registered agents. Errors are collected and joined.
// Intended for session shutdown.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for name, o := range r.plugins {
		if err := o.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close agent %q: %w", name, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	// Join errors manually to avoid importing errors (package agent has no
	// standard-library imports beyond encoding/json).
	msg := "agent registry close:"
	for _, e := range errs {
		msg += " " + e.Error() + ";"
	}
	return fmt.Errorf("%s", msg)
}

// DefaultAgentName is the alias resolved when no explicit agent is declared
// on a room effect (backwards compatibility guarantee).
const DefaultAgentName = "agent.claude"

// PluginNotSupportedError is returned when a plugin value is recognised but
// not yet fully constructed (e.g. builtin.inprocess requires programmatic injection).
type PluginNotSupportedError struct {
	Plugin string
}

func (e *PluginNotSupportedError) Error() string {
	return fmt.Sprintf("agent plugin %q not constructable from YAML — B-3 transport, inject programmatically", e.Plugin)
}
