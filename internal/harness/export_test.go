// export_test.go exposes package-internal helpers for white-box testing.
// This file is compiled only during `go test` (it lives in package harness, not harness_test).
package harness

import (
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"kitsoki/internal/app"
)

// BuildStablePrefixForTest exposes buildStablePrefix for testing.
func BuildStablePrefixForTest(appDef *app.AppDef) string {
	return buildStablePrefix(appDef)
}

// BuildDynamicSuffixForTest exposes buildDynamicSuffix for testing.
func BuildDynamicSuffixForTest(appDef *app.AppDef, in TurnInput) string {
	return buildDynamicSuffix(appDef, in)
}

// ParseTransitionArgsForTest exposes parseTransitionArgs for testing.
func ParseTransitionArgsForTest(p mcp.CallToolParams) (intent string, slots map[string]any, confidence float64) {
	return parseTransitionArgs(p)
}

// ParseValidatedPayloadForTest exposes parseValidatedPayload for testing.
func ParseValidatedPayloadForTest(raw []byte) (mcp.CallToolParams, error) {
	return parseValidatedPayload(raw)
}

// BuildClaudeArgsForTest exposes buildClaudeArgs for testing.
func BuildClaudeArgsForTest(cfg ClaudeCLIConfig) []string {
	return buildClaudeArgs(cfg, "")
}
