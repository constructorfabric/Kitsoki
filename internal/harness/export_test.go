// export_test.go exposes package-internal helpers for white-box testing.
// This file is compiled only during `go test` (it lives in package harness, not harness_test).
package harness

import (
	mcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"kitsoki/internal/app"
	"kitsoki/internal/sysprompt"
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
	return buildClaudeArgs(cfg, "", "", "", false)
}

// BuildClaudeArgsWithSystemPromptForTest exposes buildClaudeArgs with a
// system prompt set, for testing the --system-prompt override wiring.
func BuildClaudeArgsWithSystemPromptForTest(cfg ClaudeCLIConfig, systemPrompt string) []string {
	return buildClaudeArgs(cfg, "", systemPrompt, "", false)
}

// RoutingSystemPromptForTest exposes the composed routing system prompt (the
// kitsoki → project → routing layers) the harness would pass via
// --system-prompt for the given turn, without execing claude.
func RoutingSystemPromptForTest(h *ClaudeCLIHarness, in TurnInput) string {
	composed := sysprompt.Compose(sysprompt.Spec{
		Verb:    sysprompt.Route,
		Project: projectLayer(h.appDef),
		Task:    h.stablePrefix + buildSubmitInstruction(h.cfg.validatorTool()),
	})
	return composed.SystemPrompt
}
