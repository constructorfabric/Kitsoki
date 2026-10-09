// Package host — shared helpers for the agent host-call handlers.
//
// These consolidate copy/paste clusters that recurred across the agent verb
// handlers (ask, decide, ask_with_mcp, extract, task, ask_structured):
//
//   - writeMCPConfigTempfile: marshal an mcpServers map into a temp
//     --mcp-config JSON file (the marshal→CreateTemp→Write→cleanup sequence).
//   - containsBashTool / validateBashProfile: Bash-tool detection plus the
//     "Bash present but no bash_profile declared" gate.
//   - buildBaseCLIArgs: the -p / --permission-mode / --append-system-prompt /
//     --model prefix shared by ask, decide, task, and ask_structured.
//
// Behaviour is byte-for-byte identical to the inlined versions these replaced;
// these are mechanical de-duplications, not behaviour changes.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"kitsoki/internal/render/sourcecolor"
	"kitsoki/internal/sysprompt"
)

// renderAndStripPrompt renders a prompt template through pongo2 with the given
// template-args scope and strips source-color sentinels from the result. This
// is the read→render→strip core shared by the decide and task prompt
// resolvers (and structurally by ask/ask_with_mcp). Callers own the
// prompt-path-vs-inline detection and args-fallback policy.
func renderAndStripPrompt(ctx context.Context, tmpl string, templateArgs map[string]any) (string, error) {
	rendered, err := renderPromptBytes(ctx, tmpl, templateArgs)
	if err != nil {
		return "", err
	}
	return sourcecolor.Strip(rendered), nil
}

// writeMCPConfigTempfile marshals mcpServers into a {"mcpServers": …} JSON
// document, writes it to a temp file named "<prefix>-*.json", and returns the
// path plus a cleanup func the caller defers. On any error the partially
// created file is removed and a non-nil error is returned (path == "",
// cleanup == nil).
//
// The prefix mirrors the per-handler tempfile names (e.g. "kitsoki-ask-mcp",
// "kitsoki-decide-mcp") so on-disk artifacts remain attributable to a handler.
func writeMCPConfigTempfile(mcpServers map[string]any, prefix string) (string, func(), error) {
	mcpConfig := map[string]any{"mcpServers": mcpServers}
	mcpBytes, err := json.Marshal(mcpConfig)
	if err != nil {
		return "", nil, fmt.Errorf("marshal mcp config: %w", err)
	}
	f, err := os.CreateTemp("", prefix+"-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("create mcp config tempfile: %w", err)
	}
	if _, err := f.Write(mcpBytes); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", nil, fmt.Errorf("write mcp config: %w", err)
	}
	_ = f.Close()
	path := f.Name()
	return path, func() { _ = os.Remove(path) }, nil
}

// containsBashTool reports whether the (un-rewritten) tool list contains the
// built-in Bash tool.
func containsBashTool(tools []string) bool {
	for _, t := range tools {
		if t == "Bash" {
			return true
		}
	}
	return false
}

// containsBashMCPTool reports whether the (already-rewritten) tool list contains
// the kitsoki-bash MCP Bash tool — i.e. Bash was requested and rewritten by
// rewriteToolsForBashMCP. Used by the write_mode: read_only task path to decide
// whether to attach the read-only bash MCP server.
func containsBashMCPTool(tools []string) bool {
	for _, t := range tools {
		if t == "mcp__kitsoki-bash__Bash" {
			return true
		}
	}
	return false
}

// validateBashProfile enforces the Bash gate shared by ask and decide: when
// Bash is in the effective tool list the agent must declare a bash_profile.
// Returns (hasBash, errMsg); errMsg is non-empty only when Bash is present
// without a profile. verb prefixes the error message (e.g. "host.agent.ask").
func validateBashProfile(verb string, tools []string, agent Agent) (bool, string) {
	hasBash := containsBashTool(tools)
	if hasBash && agent.BashProfile == nil {
		return true, verb + ": Bash is in the tool list but the agent declares no bash_profile; " +
			"set bash_profile: read-only, commands, or sandboxed-write on the agent declaration"
	}
	return hasBash, ""
}

// buildBaseCLIArgs assembles the base `claude -p` argument prefix shared by the
// ask / decide / task handlers: -p, --permission-mode bypassPermissions, the
// layered system-prompt flags (see appendComposedSystemPrompt — verb selects
// the per-verb dynamic-sections policy), --model, and --effort (the inline
// `effort:` arg wins over the agent's, see effectiveEffort). Tools and --mcp-config are
// appended by each caller afterward (they differ in ordering and gating).
// agent_converse.go intentionally differs (session management) and does not use
// this.
func buildBaseCLIArgs(ctx context.Context, verb sysprompt.Verb, args map[string]any, agent Agent) []string {
	cliArgs := []string{
		"-p",
		"--permission-mode", effectivePermissionMode(args, agent, "bypassPermissions"),
	}
	cliArgs = appendSettingSourcesFlag(cliArgs)
	cliArgs = appendDisableSlashCommandsFlag(cliArgs)
	cliArgs = appendStrictMCPConfigFlag(cliArgs)
	cliArgs, _ = appendComposedSystemPrompt(ctx, cliArgs, verb,
		effectiveSystemPrompt(args, agent), agent.InheritClaudeDefault)
	if strings.TrimSpace(agent.Model) != "" {
		cliArgs = append(cliArgs, "--model", agent.Model)
	}
	if effort := strings.TrimSpace(effectiveEffort(args, agent)); effort != "" {
		cliArgs = append(cliArgs, "--effort", effort)
	}
	// Hard-deny AskUserQuestion on every ask/decide/task subprocess: headless
	// `-p` has no TTY, so the CLI auto-resolves it with empty answers and the
	// model proceeds on a guess (see alwaysDeniedTools).
	cliArgs = appendDisallowedToolsFlag(cliArgs, alwaysDeniedTools)
	cliArgs = appendDisallowedToolsFlag(cliArgs, effectiveDisallowedTools(args, agent))
	return cliArgs
}
