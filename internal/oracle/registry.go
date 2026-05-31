// registry.go holds the per-app oracle plugin registry.
//
// A Registry maps plugin alias names (e.g. "oracle.claude",
// "oracle.autofix_fixer") to Oracle implementations. It is constructed at
// session-construction time from the app's oracle_plugins declarations (see
// BuildRegistryFromDef) and injected into the dispatch context.

package oracle

import (
	"fmt"
	"sync"
)

// Registry holds the per-app oracle plugins keyed by alias name
// (the name used in the `hosts:` YAML block, e.g. "oracle.claude").
// It is safe for concurrent read from multiple goroutines; mutations
// happen only at construction time (single-threaded).
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Oracle
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{plugins: make(map[string]Oracle)}
}

// Register stores o under name. Panics on duplicate registration so
// misconfigured stories fail fast at boot rather than silently shadowing.
func (r *Registry) Register(name string, o Oracle) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.plugins[name]; exists {
		panic(fmt.Sprintf("oracle registry: duplicate plugin %q", name))
	}
	r.plugins[name] = o
}

// Get returns the Oracle registered under name, or (nil, false) when not found.
func (r *Registry) Get(name string) (Oracle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	o, ok := r.plugins[name]
	return o, ok
}

// Resolve returns the Oracle for name, falling back to "oracle.claude" when
// name is empty or when the specific alias is absent. Returns (nil, error)
// when neither the named alias nor the default is registered.
func (r *Registry) Resolve(name string) (Oracle, error) {
	if name == "" {
		name = DefaultOracleName
	}
	r.mu.RLock()
	o, ok := r.plugins[name]
	r.mu.RUnlock()
	if ok {
		return o, nil
	}
	// Fall back to the default oracle when the requested alias is absent.
	if name != DefaultOracleName {
		r.mu.RLock()
		o, ok = r.plugins[DefaultOracleName]
		r.mu.RUnlock()
		if ok {
			return o, nil
		}
	}
	return nil, fmt.Errorf("oracle registry: no oracle registered for %q (and no default %q fallback)", name, DefaultOracleName)
}

// Close closes all registered oracles. Errors are collected and joined.
// Intended for session shutdown.
func (r *Registry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for name, o := range r.plugins {
		if err := o.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close oracle %q: %w", name, err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	// Join errors manually to avoid importing errors (package oracle has no
	// standard-library imports beyond encoding/json).
	msg := "oracle registry close:"
	for _, e := range errs {
		msg += " " + e.Error() + ";"
	}
	return fmt.Errorf("%s", msg)
}

// DefaultOracleName is the alias resolved when no explicit oracle is declared
// on a room effect (backwards compatibility guarantee).
const DefaultOracleName = "oracle.claude"

// PluginNotSupportedError is returned when a plugin value is recognised but
// not yet fully constructed (e.g. builtin.inprocess requires programmatic injection).
type PluginNotSupportedError struct {
	Plugin string
}

func (e *PluginNotSupportedError) Error() string {
	return fmt.Sprintf("oracle plugin %q not constructable from YAML — B-3 transport, inject programmatically", e.Plugin)
}
