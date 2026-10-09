package main

import (
	"os"
	"strconv"
	"strings"

	"kitsoki/internal/orchestrator"
)

// semanticRoutingFlag backs the global --semantic-routing persistent flag and
// semanticRoutingFlagSet records whether the operator passed it (set in the
// root PersistentPreRunE). Together with KITSOKI_SEMANTIC_ROUTING they drive an
// optional orchestrator-level override for the deterministic semantic-routing
// stack.
var (
	semanticRoutingFlag    bool
	semanticRoutingFlagSet bool
)

// semanticRoutingOptions resolves the semantic-routing toggle and returns the
// orchestrator option that wires the process-level override.
// Precedence (highest first):
//
//  1. --semantic-routing (when the operator passed it explicitly)
//  2. KITSOKI_SEMANTIC_ROUTING (1/true/on / 0/false/off, case-insensitive)
//  3. unset: no override at all — defer to the per-app routing.enabled
//     config (default true; see app.DefaultRoutingConfig), exactly as
//     orchestrator.WithSemanticRouting's doc comment promises.
//
// This used to force an override to "disabled" even when neither the flag
// nor the env var were set, silently overriding every app's own
// routing.enabled — including dev-story/bugfix/prd, which all default (or
// explicitly opt) to enabled. That meant every live surface built through
// buildSessionRuntime (kitsoki web, kitsoki run/TUI, kitsoki turn, MCP
// drive/submit) ran with the deterministic semantic-routing stack OFF by
// default, while `kitsoki test routing` (internal/testrunner/routing.go)
// builds its orchestrator with no options at all and so correctly honoured
// the per-app default — a fixture-vs-live divergence invisible to the
// routing-tuning fixtures (see .context/dwf2-d1-findings.md). WS-C's own
// decision was to "start semantic routing at a reasonable baseline" — the
// per-app config is that baseline; the CLI flag/env stay a live override in
// either direction, not a silent default.
func semanticRoutingOptions() []orchestrator.Option {
	enabled, ok := semanticRoutingOverride()
	if !ok {
		return nil
	}
	return []orchestrator.Option{orchestrator.WithSemanticRouting(enabled)}
}

// semanticRoutingOverride resolves the command/env override. ok=false means
// "unset; defer to per-app routing.enabled".
func semanticRoutingOverride() (enabled bool, ok bool) {
	if semanticRoutingFlagSet {
		return semanticRoutingFlag, true
	}
	if v, ok := os.LookupEnv("KITSOKI_SEMANTIC_ROUTING"); ok {
		if b, err := parseEnvBool(v); err == nil {
			return b, true
		}
	}
	return false, false
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
