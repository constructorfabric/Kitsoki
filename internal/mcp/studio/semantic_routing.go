package studio

import (
	"os"
	"strconv"
	"strings"

	"kitsoki/internal/orchestrator"
)

// semanticRoutingEnvOption resolves the deterministic semantic-routing toggle
// from KITSOKI_SEMANTIC_ROUTING. It returns (option, true) only when the env var
// is explicitly set; when absent it returns ok=false so the caller leaves the
// orchestrator to defer to the per-app routing.enabled config. The `kitsoki mcp`
// command exports this env var from the global --semantic-routing flag (default
// false → free-text routing is an isolated main-model decision); flow/cassette
// tests that open the studio directly leave it unset and keep their deterministic
// routing fixtures. See docs/architecture/semantic-routing.md.
func semanticRoutingEnvOption() (orchestrator.Option, bool) {
	v, ok := os.LookupEnv("KITSOKI_SEMANTIC_ROUTING")
	if !ok {
		return nil, false
	}
	enabled, err := parseSemanticRoutingBool(v)
	if err != nil {
		return nil, false
	}
	return orchestrator.WithSemanticRouting(enabled), true
}

// parseSemanticRoutingBool accepts the usual truthy/falsey spellings plus on/off.
func parseSemanticRoutingBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "on", "yes":
		return true, nil
	case "off", "no":
		return false, nil
	}
	return strconv.ParseBool(strings.TrimSpace(v))
}
