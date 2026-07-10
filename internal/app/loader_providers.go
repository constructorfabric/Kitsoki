// Package app — provider declaration loader.
//
// See docs/guide/agents/providers.md for the declaration format.
//
// resolveProviders is called after parseAndMerge / resolveImports to:
//  1. Validate every ProviderDecl in def.Providers (non-empty name; a provider
//     that sets none of backend:, model:, effort:, or env: is useless and rejected).
//  2. Perform single-pass ${VAR} substitution in each provider's Env map,
//     reusing the same expandEnvVar contract as agent_plugins. Unset env vars
//     are hard errors.
//
// Reference validation (an agent's provider: and an effect's
// with: { provider: } pointing at a declared provider) runs separately in
// validateProviderReferences, alongside the agent-reference checks.
package app

import (
	"fmt"
	"strings"
)

// validEffortLevels are the values claude's --effort flag accepts. An empty
// effort is always allowed (it leaves the CLI default); only a non-empty value
// outside this set is rejected at load time.
var validEffortLevels = map[string]struct{}{
	"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
}

var validProviderBackends = map[string]struct{}{
	"claude": {}, "codex": {}, "copilot": {},
}

// validateEffort reports an error message when effort is non-empty and not one
// of validEffortLevels, or "" when the value is acceptable. site prefixes the
// message (e.g. `agent "judge"` or `providers.openrouter`).
func validateEffort(site, effort string) string {
	e := strings.TrimSpace(effort)
	if e == "" {
		return ""
	}
	if _, ok := validEffortLevels[e]; !ok {
		return fmt.Sprintf("%s: effort %q is invalid (valid: low, medium, high, xhigh, max)", site, effort)
	}
	return ""
}

func validateProviderBackend(site, backend string) string {
	b := strings.TrimSpace(backend)
	if b == "" {
		return ""
	}
	if _, ok := validProviderBackends[b]; !ok {
		return fmt.Sprintf("%s: backend %q is invalid (valid: claude, codex, copilot)", site, backend)
	}
	return ""
}

// resolveProviders validates and resolves all provider declarations. It must be
// called after parseAndMerge. Errors are returned (not appended to a shared
// slice) so the caller threads them the same way as resolveAgentPlugins.
func resolveProviders(def *AppDef, file string) []error {
	if def == nil || len(def.Providers) == 0 {
		return nil
	}
	var errs []error
	addErr := func(msg string) {
		errs = append(errs, &ValidationError{File: file, Message: msg})
	}

	for name, decl := range def.Providers {
		if decl == nil {
			addErr(fmt.Sprintf("providers.%s: empty declaration", name))
			continue
		}
		if strings.TrimSpace(decl.Backend) == "" && strings.TrimSpace(decl.Model) == "" && len(decl.Env) == 0 && strings.TrimSpace(decl.Effort) == "" {
			addErr(fmt.Sprintf("providers.%s: a provider must set backend:, model:, effort:, and/or env: (an empty provider has no effect)", name))
			continue
		}
		if msg := validateProviderBackend(fmt.Sprintf("providers.%s", name), decl.Backend); msg != "" {
			addErr(msg)
			continue
		}
		if msg := validateEffort(fmt.Sprintf("providers.%s", name), decl.Effort); msg != "" {
			addErr(msg)
			continue
		}
		for k, v := range decl.Env {
			expanded, missing := expandEnvVar(v)
			if missing != "" {
				addErr(fmt.Sprintf("providers.%s: env var %s referenced in env.%s not set", name, missing, k))
				continue
			}
			decl.Env[k] = expanded
		}
	}
	return errs
}

// validateProviderReferences asserts that every site selecting a provider by
// name (agents.<name>.provider, and effect with.provider) resolves to an entry
// in def.Providers. Unknown references produce one error per site naming the
// known providers. Templated values (containing "{{") are skipped — they are
// resolved at runtime and cannot be checked statically.
func validateProviderReferences(file string, def *AppDef, errs *[]error) {
	if def == nil {
		return
	}
	known := make(map[string]struct{}, len(def.Providers))
	for name := range def.Providers {
		known[name] = struct{}{}
	}
	knownStr := strings.Join(sortedStringKeys(known), ", ")
	if knownStr == "" {
		knownStr = "(none declared)"
	}
	addUnknown := func(name, site string) {
		*errs = append(*errs, &ValidationError{
			File: file,
			Message: fmt.Sprintf(
				"provider reference %q at %s is undefined (known providers: %s)",
				name, site, knownStr,
			),
		})
	}

	// agents.<name>.provider
	for _, agentName := range sortedKeys(def.Agents) {
		a := def.Agents[agentName]
		if a == nil || a.Provider == "" || strings.Contains(a.Provider, "{{") {
			continue
		}
		if _, ok := known[a.Provider]; !ok {
			addUnknown(a.Provider, fmt.Sprintf("agents.%s.provider", agentName))
		}
	}

	// effect with.provider — only meaningful on host.agent.* invocations.
	walkAllEffects(def.States, func(loc string, eff Effect) {
		if eff.With == nil {
			return
		}
		raw, ok := eff.With["provider"]
		if !ok {
			return
		}
		name, ok := raw.(string)
		if !ok || name == "" || strings.Contains(name, "{{") {
			return
		}
		if eff.Invoke != "" && !strings.HasPrefix(eff.Invoke, "host.agent.") {
			*errs = append(*errs, &ValidationError{
				File:    file,
				Message: fmt.Sprintf("%s: with.provider is only meaningful on host.agent.* invocations (got invoke %q)", loc, eff.Invoke),
			})
			return
		}
		if _, found := known[name]; !found {
			addUnknown(name, loc+" with.provider")
		}
	})
}

// sortedStringKeys returns the keys of a set as a sorted slice. Local helper so
// this file doesn't depend on the generic sortedKeys's map-value type.
func sortedStringKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Reuse the package's stable sort via the standard library indirectly.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
