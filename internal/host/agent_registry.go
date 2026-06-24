// Package host — agent registry plumbing for the per-call `agent:` argument
// on host.agent.ask_with_mcp (and any future site that wants to dispatch
// against a named agent).
//
// Design note (WS-A7): we use a package-level setter rather than a context
// key because the registry is constructed once at startup from AppDef.Agents
// + builtins and never varies per request. A context key would force every
// dispatch site (orchestrator, tests, MCP server) to thread the value
// through context.WithValue plumbing for a value that is effectively
// process-global. The handler is a free function (not a closure over a
// HostCfg struct), so a struct field would have required touching every
// RegisterBuiltins call site and the registry interface itself; the setter
// is the lowest-blast-radius option.
//
// Tests swap in a fake registry via SetAgentRegistry and restore the prior
// value (or nil) on cleanup. Concurrent reads after a single startup write
// are safe via the RWMutex.
package host

import (
	"sync"

	"kitsoki/internal/agents"
)

var (
	agentRegistryMu sync.RWMutex
	agentRegistry   agents.Registry
)

// SetAgentRegistry installs the process-wide agent registry. Called once at
// startup after the AppDef is loaded; tests may call it to swap in a fake
// (use t.Cleanup with the prior value to restore).
//
// Passing nil clears the registry; subsequent calls to AgentRegistry return
// nil and any `agent:` arg on a handler will fail with a clear error.
func SetAgentRegistry(r agents.Registry) {
	agentRegistryMu.Lock()
	defer agentRegistryMu.Unlock()
	agentRegistry = r
}

// AgentRegistry returns the process-wide agent registry, or nil if none has
// been set. Handlers that honour the `agent:` arg call this; nil here means
// "no app loaded / no registry wired" and the handler should surface a
// clear error rather than silently fall back.
func AgentRegistry() agents.Registry {
	agentRegistryMu.RLock()
	defer agentRegistryMu.RUnlock()
	return agentRegistry
}
