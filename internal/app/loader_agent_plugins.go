// Package app — agent plugin declaration loader.
//
// See docs/architecture/agent-plugin.md for the declaration format and the
// resolution rules summarised below.
//
// resolveAgentPlugins is called after parseAndMerge / resolveImports to:
//  1. Validate every AgentPluginDecl in def.AgentPlugins.
//  2. Perform single-pass ${VAR} substitution in each plugin's Env and Headers
//     maps. Unset env vars are hard errors.
//  3. Inject a default "agent.claude" entry (plugin: builtin.claude_cli) when
//     the story omits agent_plugins: entirely or omits agent.claude specifically,
//     so all existing stories run unchanged.
//
// The resolved declarations stay on def.AgentPlugins; the host.AgentRegistry
// is built from them by the orchestrator at session construction.
package app

import (
	"fmt"
	"os"
	"strings"
)

// knownPlugins is the set of all plugin values supported (B-2 builtins + B-3 transports).
var knownPlugins = map[string]bool{
	"builtin.claude_cli": true,
	"builtin.inprocess":  true,
	"subprocess":         true,
	"mcp_http":           true,
	"builtin.local_llm":  true,
}

// resolveAgentPlugins validates and resolves all agent plugin declarations.
// It must be called after parseAndMerge. Errors are appended to *errs.
func resolveAgentPlugins(def *AppDef, file string) []error {
	var errs []error
	addErr := func(msg string) {
		errs = append(errs, &ValidationError{File: file, Message: msg})
	}

	if def.AgentPlugins == nil {
		def.AgentPlugins = make(map[string]*AgentPluginDecl)
	}

	// Validate and resolve each declared plugin.
	for name, decl := range def.AgentPlugins {
		if decl == nil {
			addErr(fmt.Sprintf("agent_plugins.%s: empty declaration", name))
			continue
		}
		if !strings.HasPrefix(name, "agent.") {
			addErr(fmt.Sprintf("agent_plugins: key %q must start with 'agent.' prefix", name))
			continue
		}
		if decl.Plugin == "" {
			addErr(fmt.Sprintf("agent_plugins.%s: plugin: is required", name))
			continue
		}
		if !knownPlugins[decl.Plugin] {
			addErr(fmt.Sprintf("agent_plugins.%s: unknown plugin %q (supported: builtin.claude_cli, builtin.inprocess, subprocess, mcp_http, builtin.local_llm)", name, decl.Plugin))
			continue
		}
		// subprocess transport requires command:.
		if decl.Plugin == "subprocess" && strings.TrimSpace(decl.Command) == "" {
			addErr(fmt.Sprintf("agent_plugins.%s: subprocess plugin requires command:", name))
			continue
		}
		// mcp_http transport requires endpoint:.
		if decl.Plugin == "mcp_http" && strings.TrimSpace(decl.Endpoint) == "" {
			addErr(fmt.Sprintf("agent_plugins.%s: mcp_http plugin requires endpoint:", name))
			continue
		}
		// builtin.local_llm requires either model: (managed sidecar) or
		// endpoint: (bring-your-own-server).
		if decl.Plugin == "builtin.local_llm" && strings.TrimSpace(decl.Model) == "" && strings.TrimSpace(decl.Endpoint) == "" {
			addErr(fmt.Sprintf("agent_plugins.%s: builtin.local_llm plugin requires model: or endpoint:", name))
			continue
		}
		// Interpolate ${VAR} in Env map.
		for k, v := range decl.Env {
			expanded, missing := expandEnvVar(v)
			if missing != "" {
				addErr(fmt.Sprintf("agent_plugins.%s: env var %s referenced in env.%s not set", name, missing, k))
				continue
			}
			decl.Env[k] = expanded
		}
		// Interpolate ${VAR} in Headers map.
		for k, v := range decl.Headers {
			expanded, missing := expandEnvVar(v)
			if missing != "" {
				addErr(fmt.Sprintf("agent_plugins.%s: env var %s referenced in headers.%s not set", name, missing, k))
				continue
			}
			decl.Headers[k] = expanded
		}
	}

	if len(errs) > 0 {
		return errs
	}

	// Inject default agent.claude if missing.
	if _, hasDefault := def.AgentPlugins["agent.claude"]; !hasDefault {
		def.AgentPlugins["agent.claude"] = &AgentPluginDecl{Plugin: "builtin.claude_cli"}
	}

	return nil
}

// expandEnvVar performs a single-pass ${VAR} substitution in s.
// Returns (expanded, "") on success.
// Returns ("", "VAR") when any ${VAR} token references an unset env var.
//
// Single-pass means the scanner moves strictly left-to-right through the
// original string.  When a ${VAR} token is expanded, the replacement value is
// written to the output buffer but the scanner does NOT re-scan it — any ${
// sequences inside a replacement value pass through verbatim.  This prevents
// injection attacks and matches the documented ${VAR} substitution contract.
func expandEnvVar(s string) (expanded, missing string) {
	var buf strings.Builder
	i := 0
	for i < len(s) {
		// Find the next "${"
		idx := strings.Index(s[i:], "${")
		if idx < 0 {
			// No more tokens; copy remainder verbatim.
			buf.WriteString(s[i:])
			break
		}
		// Copy everything before this token verbatim.
		buf.WriteString(s[i : i+idx])
		i += idx + 2 // skip past "${"

		// Find the matching closing "}"
		end := strings.Index(s[i:], "}")
		if end < 0 {
			// No closing brace; treat "${" and the rest as literal.
			buf.WriteString("${")
			// Continue scanning — i already points past "${"
			continue
		}
		varName := s[i : i+end]
		i += end + 1 // skip past varName + "}"

		val, ok := os.LookupEnv(varName)
		if !ok {
			return "", varName
		}
		// Write the replacement value verbatim (NOT re-scanned).
		buf.WriteString(val)
	}
	return buf.String(), ""
}
