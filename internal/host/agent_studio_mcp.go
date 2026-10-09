// Package host — auto-attachment of the kitsoki studio MCP server onto
// claude-backend dispatches whose declared tool surface names it.
//
// Context (Task 1.3 precondition fix, docs/architecture/room-workbench.md): a
// live precheck proved that a claude-backend `-p` subprocess NEVER sees the
// kitsoki studio MCP server (the one the project's own .mcp.json calls
// "kitsoki", exposing mcp__kitsoki__studio_ping / studio_handles / studio_work
// / etc.) even when the agent's tool list names one of its tools —
// appendStrictMCPConfigFlag (agents.go) deliberately makes --mcp-config the
// ONLY source of MCP servers for the subprocess (protecting the validator
// `submit` tool from ambient project .mcp.json interference), but no call
// site ever added a "kitsoki" entry to that --mcp-config file. Naming the
// tool in an agent's `tools:` (or a toolbox's `tools:`) was therefore
// necessary but not sufficient: --allowedTools would permit the tool, but no
// server ever backed it, so the subprocess could not discover or call it.
//
// Fix: gate the injection on the SAME toolbox/effect vocabulary that already
// governs every other permission decision in this package — a tool name
// under the mcp__kitsoki__ namespace is, by construction, a declaration that
// the dispatch needs the studio server, exactly the way mcp__kitsoki-bash__Bash
// and mcp__validator__submit already signal the bash and validator servers.
// No new boolean flag, no new permission surface: the WS toolbox/effect
// declaration remains the only permission mechanism.
package host

import (
	"os"
	"strings"
)

// kitsokiStudioServerName is the --mcp-config key for the kitsoki studio MCP
// server — the same key name the project's own .mcp.json uses for it.
const kitsokiStudioServerName = "kitsoki"

// kitsokiStudioToolPrefix namespaces every tool the kitsoki studio MCP server
// exposes (mcp__kitsoki__studio_ping, mcp__kitsoki__story_read, ...). Any
// agent/toolbox tool list naming a tool under this prefix is, by the same
// convention as mcp__kitsoki-bash__Bash / mcp__validator__submit, declaring
// that it needs the corresponding server attached.
const kitsokiStudioToolPrefix = "mcp__kitsoki__"

// kitsokiStudioStoriesDirEnv overrides the --stories-dir argument passed to
// the auto-attached studio server (tests / non-standard checkout layouts).
// Empty uses the "stories" convention the project's own .mcp.json uses.
const kitsokiStudioStoriesDirEnv = "KITSOKI_STUDIO_STORIES_DIR"

// declaresStudioMCPTools reports whether tools names any tool under the
// kitsoki studio server's own namespace.
func declaresStudioMCPTools(tools []string) bool {
	for _, t := range tools {
		if strings.HasPrefix(t, kitsokiStudioToolPrefix) {
			return true
		}
	}
	return false
}

// kitsokiStudioMCPServerEntry returns the {"command","args"} --mcp-config
// entry that drops this kitsoki binary in as its own studio MCP server —
// resolved the exact same way the project's tracked .mcp.json resolves it:
// `kitsoki mcp --stories-dir <dir>`, "kitsoki" found on the subprocess's own
// PATH, with a RELATIVE stories dir resolved against the dispatched agent
// subprocess's own working directory. Never a hardcoded absolute path.
func kitsokiStudioMCPServerEntry() map[string]any {
	storiesDir := "stories"
	if v := strings.TrimSpace(os.Getenv(kitsokiStudioStoriesDirEnv)); v != "" {
		storiesDir = v
	}
	return map[string]any{
		"command": "kitsoki",
		"args":    []any{"mcp", "--stories-dir", storiesDir},
	}
}

// attachStudioMCPServer adds the "kitsoki" studio MCP server entry to
// mcpServers when the effective tool surface (post toolbox/agent resolution)
// declares any mcp__kitsoki__* tool, AND no caller-supplied "kitsoki" entry
// already exists — an explicit mcp.servers.kitsoki declaration on the agent
// always wins over this auto-attach. Returns mcpServers unchanged (including
// nil) when no studio tool is declared, so callers that gate "write the
// --mcp-config file at all" on `len(mcpServers) > 0` must call this BEFORE
// that check, not after, or a studio-only agent with no other MCP server
// would silently skip --mcp-config entirely.
//
// This is the single seam every claude-backend call site applies consistently
// (ask, ask_with_mcp, decide, task, converse, ask_structured,
// operator_ask_bridge) so a toolbox naming a studio tool works the same way
// regardless of which agent verb dispatches it.
func attachStudioMCPServer(mcpServers map[string]any, tools []string) map[string]any {
	if !declaresStudioMCPTools(tools) {
		return mcpServers
	}
	if mcpServers == nil {
		mcpServers = make(map[string]any, 1)
	}
	if _, exists := mcpServers[kitsokiStudioServerName]; !exists {
		mcpServers[kitsokiStudioServerName] = kitsokiStudioMCPServerEntry()
	}
	return mcpServers
}
