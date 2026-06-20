package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	studio "kitsoki/internal/mcp/studio"
)

// mcpCmd starts the kitsoki studio MCP server on stdio. Unlike `kitsoki serve`
// (which exposes one app's `transition` tool), `kitsoki mcp` exposes the studio
// facade: a server whose state is an authoring workspace plus 0..n driving
// sessions, defaulting every session to the no-LLM replay harness.
//
// This slice ships the server core (transport, handle model, tool registry) with
// a trivial studio.ping / studio.handles pair; the domain tools (story.* /
// session.* / render.*) land in later slices.
//
// Example (smoke test via shell):
//
//	echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{...}}' \
//	  | kitsoki mcp --stories-dir ./stories
func mcpCmd() *cobra.Command {
	var (
		storiesDir  string
		dbPath      string
		harnessType string
		workspace   string
	)
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Start the kitsoki studio MCP server on stdio",
		Long: `Start the kitsoki studio MCP server on stdin/stdout. An external coding
agent (Claude Code, Claude Desktop) attaches and authors a story, drives a
session, and sees the result through a single studio facade.

Unlike 'kitsoki serve' (one app's 'transition' tool), the studio server holds an
authoring workspace handle and 0..n driving-session handles, and defaults every
session to the no-LLM replay harness (--harness replay). Pass --harness live to
opt a session into a real LLM.

Tools: the studio.ping liveness probe and studio.handles lister (server core);
the deterministic story.read/write/validate/graph/test authoring tools; and the
session.new/attach/drive/submit/continue/inspect/trace driving tools plus
render.tui/tui_png/web. Driving defaults to harness:replay (no LLM); render.tui
and render.tui_png return the terminal Frame / PNG, while render.web degrades to
a text result unless a browser-capable web shot is wired (deferred — it needs a
served kitsoki web, the hybrid-session-driving concern).

Attach by adding to a client's .mcp.json (see 'kitsoki docs' once the studio
docs land):

  { "mcpServers": { "kitsoki": { "command": "kitsoki",
      "args": ["mcp", "--stories-dir", "<dir>"] } } }`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dbPath == "" {
				dbPath = defaultDBPath()
			}

			// Build the studio session. A nil HarnessBuilder falls back to the
			// production DefaultHarnessBuilder (replay → no LLM; live deferred to
			// the driving slice).
			sess := studio.NewStudioSession(nil)

			// Optionally bind an initial authoring workspace. Loading is
			// best-effort: a load/validation error is cached on the handle (so a
			// story.* tool can surface it) rather than aborting the server boot —
			// the agent may want to attach precisely to fix a broken story.
			if workspace != "" {
				def, loadErr := app.Load(workspace)
				if _, err := sess.OpenWorkspace(studio.OpenWorkspaceParams{
					Dir:     workspace,
					Def:     def,
					LoadErr: loadErr,
				}); err != nil {
					return fmt.Errorf("mcp: open workspace %q: %w", workspace, err)
				}
			}

			srv := studio.NewServer(sess)

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
			defer cancel()

			fmt.Fprintf(os.Stderr, "kitsoki: studio MCP server on stdio (harness default %q, stories %q)\n",
				harnessType, storiesDir)
			return srv.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&storiesDir, "stories-dir", "",
		"root for story.* workspace resolution (mirrors `kitsoki web`)")
	cmd.Flags().StringVar(&dbPath, "db", "",
		"path to the SQLite session database for driving handles (default: the shared kitsoki sessions.db)")
	cmd.Flags().StringVar(&harnessType, "harness", "replay",
		"default harness for driving sessions: replay|live (default replay → no LLM; per-session override on session.new)")
	cmd.Flags().StringVar(&workspace, "workspace", "",
		"optional initial authoring workspace (a story dir or app.yaml) bound as the workspace handle on boot")
	return cmd
}

// mcpAttachEntry builds the .mcp.json mcpServers entry that drops this binary in
// as the "kitsoki" studio server. It mirrors the writeMCPConfigTempfile shape in
// internal/host/agent_helpers.go: {"mcpServers": {"kitsoki": {"command": ...,
// "args": ["mcp", "--stories-dir", ...]}}}. command is the kitsoki binary path;
// storiesDir is forwarded as --stories-dir (omitted when empty).
func mcpAttachEntry(command, storiesDir string) map[string]any {
	args := []any{"mcp"}
	if storiesDir != "" {
		args = append(args, "--stories-dir", storiesDir)
	}
	return map[string]any{
		"mcpServers": map[string]any{
			"kitsoki": map[string]any{
				"command": command,
				"args":    args,
			},
		},
	}
}

// mcpAttachJSON marshals the .mcp.json attach entry (mcpAttachEntry) to indented
// JSON — the snippet a user pastes into a client's config.
func mcpAttachJSON(command, storiesDir string) ([]byte, error) {
	return json.MarshalIndent(mcpAttachEntry(command, storiesDir), "", "  ")
}
