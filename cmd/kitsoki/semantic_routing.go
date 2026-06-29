package main

import (
	"os"
	"strconv"
	"strings"

	"kitsoki/internal/orchestrator"
)

// semanticRoutingFlag backs the global --semantic-routing persistent flag and
// semanticRoutingFlagSet records whether the operator passed it (set in the
// root PersistentPreRunE). Together with KITSOKI_SEMANTIC_ROUTING they drive the
// orchestrator-level toggle for the deterministic semantic-routing stack.
var (
	semanticRoutingFlag    bool
	semanticRoutingFlagSet bool
)

// semanticRoutingOption resolves the semantic-routing toggle and returns the
// orchestrator option that wires it. Precedence (highest first):
//
//  1. --semantic-routing (when the operator passed it explicitly)
//  2. KITSOKI_SEMANTIC_ROUTING (1/true/on / 0/false/off, case-insensitive)
//  3. default: false — free-text routing is an isolated main-model decision
//     (harness.RunTurn) and the deterministic stack (semroute + turn-cache +
//     default_intent sink + free-form fallback) is skipped. The zero-cost exact
//     display/example match still runs.
//
// Every CLI surface that builds a free-text-routing orchestrator appends this
// option so the LLM-only default holds in production; the per-app routing.enabled
// config only takes effect for orchestrators built without it (tests, the flow
// runner). See docs/architecture/semantic-routing.md.
func semanticRoutingOption() orchestrator.Option {
	return orchestrator.WithSemanticRouting(semanticRoutingEnabled())
}

// semanticRoutingEnabled resolves the toggle to a concrete bool using the
// precedence documented on semanticRoutingOption.
func semanticRoutingEnabled() bool {
	enabled := false
	if v, ok := os.LookupEnv("KITSOKI_SEMANTIC_ROUTING"); ok {
		if b, err := parseEnvBool(v); err == nil {
			enabled = b
		}
	}
	if semanticRoutingFlagSet {
		enabled = semanticRoutingFlag
	}
	return enabled
}

// parseEnvBool accepts the usual truthy/falsey spellings plus on/off.
func parseEnvBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "yes":
		return true, nil
	case "off", "no":
		return false, nil
	}
	return strconv.ParseBool(strings.TrimSpace(v))
}
