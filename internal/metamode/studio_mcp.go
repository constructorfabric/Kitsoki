package metamode

import (
	"fmt"
	"os"
)

// kitsokiBinaryEnv overrides the path to the kitsoki binary used to spawn the
// studio MCP server. Mirrors the same env in internal/host so a test (or a
// `go run` parent) can point at an explicit binary. When unset we fall back to
// os.Executable() — the running kitsoki process re-invokes its own `mcp`
// subcommand, exactly as the validator MCP server does.
const kitsokiBinaryEnv = "KITSOKI_BIN"

// studioMCPName is the mcp_servers key. The Go SDK exposes the studio tools to
// the agent as mcp__<key>__<tool> (e.g. mcp__kitsoki__story.validate), so the
// key here must match what the agent prompts reference.
const studioMCPName = "kitsoki"

// studioMCPServers builds the mcp_servers map that attaches the kitsoki studio
// server to a meta-mode agent's claude subprocess, scoped to storyDir (bound as
// the studio workspace so story.* tools default to it). readOnly omits
// story.write — the Q&A surface keeps the read + replay-driving tools but cannot
// edit the story tree. Driving defaults to the no-LLM replay harness.
//
// The binary is resolved from KITSOKI_BIN or os.Executable(); a resolution
// failure returns an error the caller treats as "attach nothing" rather than
// aborting the turn — the agent degrades to its plain Read/Edit toolset.
func studioMCPServers(storyDir string, readOnly bool) (map[string]any, error) {
	if storyDir == "" {
		return nil, fmt.Errorf("metamode: studio MCP needs a story dir")
	}
	bin := os.Getenv(kitsokiBinaryEnv)
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("metamode: locate kitsoki binary: %w", err)
		}
		bin = exe
	}

	args := []any{"mcp", "--workspace", storyDir, "--harness", "replay"}
	if readOnly {
		args = append(args, "--read-only")
	}

	return map[string]any{
		studioMCPName: map[string]any{
			"command": bin,
			"args":    args,
		},
	}, nil
}
