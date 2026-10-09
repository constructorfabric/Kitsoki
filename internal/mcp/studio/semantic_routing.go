package studio

import (
	"os"
	"strconv"
	"strings"

	"kitsoki/internal/orchestrator"
)

// semanticRoutingEnvOption resolves the deterministic semantic-routing toggle
// from KITSOKI_SEMANTIC_ROUTING. When the env var is absent it defaults to
// false, matching the CLI's deterministic-then-harness posture.
func semanticRoutingEnvOption() (orchestrator.Option, bool) {
	v, ok := os.LookupEnv("KITSOKI_SEMANTIC_ROUTING")
	if !ok {
		return orchestrator.WithSemanticRouting(false), true
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
