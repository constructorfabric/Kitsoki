// mcp_operator_ask.go — implements `kitsoki mcp-operator-ask`.
//
// Runs an MCP stdio server exposing one tool — by default named "ask" — that
// forwards a multiple-choice question to the live kitsoki operator and returns
// their answer to the calling model. It is the supported replacement for the
// built-in AskUserQuestion tool, which is hard-denied on every agent
// subprocess (headless `claude -p` has no TTY, so AskUserQuestion auto-resolves
// with empty answers — anthropics/claude-code#50728).
//
// The server does not answer the question itself: each `ask` call is forwarded
// over the per-call unix socket at --socket (or $KITSOKI_OPERATOR_ASK_SOCK) to
// the kitsoki host handler that spawned the agent, which surfaces it on the
// web/TUI surface, collects the operator's answer, and returns it. The host side
// of that socket is wired in phase 3; this subcommand is auto-attached by the
// agent dispatch layer (via --mcp-config) only when a live operator surface is
// attached to the session.
//
// Example claude --mcp-config entry:
//
//	{
//	  "mcpServers": {
//	    "operator": {
//	      "command": "kitsoki",
//	      "args": ["mcp-operator-ask", "--socket", "/tmp/kitsoki-opask-123.sock"]
//	    }
//	  }
//	}
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	kitsokimcp "kitsoki/internal/mcp"
)

// operatorAskSocketEnv is the environment variable the agent dispatch layer
// sets on the subprocess so the auto-attached server finds its per-call socket
// without the MCP-config args needing to embed an absolute path.
const operatorAskSocketEnv = "KITSOKI_OPERATOR_ASK_SOCK"

func mcpOperatorAskCmd() *cobra.Command {
	var (
		socketPath  string
		toolName    string
		description string
	)
	cmd := &cobra.Command{
		Use:   "mcp-operator-ask",
		Short: "Run a stdio MCP server that forwards questions to the kitsoki operator",
		Long: `mcp-operator-ask runs an MCP stdio server exposing one tool — by
default named "ask" — the supported replacement for the built-in
AskUserQuestion tool. Each call forwards a multiple-choice question over a
unix socket to the kitsoki host that spawned this agent; the host surfaces it
to the live operator (web/TUI), collects the answer, and returns it to the
model. The call blocks until the operator answers (or the host-side wait times
out / is cancelled, in which case an LLM-visible error is returned so the agent
proceeds on its own).

The socket path comes from --socket or, when omitted, the
` + operatorAskSocketEnv + ` environment variable (set by the agent dispatch
layer on the subprocess).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sock := socketPath
			if sock == "" {
				sock = os.Getenv(operatorAskSocketEnv)
			}
			if sock == "" {
				return fmt.Errorf("either --socket or $%s is required", operatorAskSocketEnv)
			}

			srv, err := kitsokimcp.NewOperatorAskServer(kitsokimcp.OperatorAskConfig{
				SocketPath:      sock,
				ToolName:        toolName,
				ToolDescription: description,
			})
			if err != nil {
				return fmt.Errorf("build operator-ask server: %w", err)
			}

			ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()

			fmt.Fprintf(os.Stderr, "kitsoki: mcp-operator-ask stdio server (socket=%s)\n", sock)
			return srv.Run(ctx)
		},
	}
	cmd.Flags().StringVar(&socketPath, "socket", "",
		"unix socket to forward questions to (default: $"+operatorAskSocketEnv+")")
	cmd.Flags().StringVar(&toolName, "tool-name", "", `override the tool name (default: "ask")`)
	cmd.Flags().StringVar(&description, "tool-description", "",
		"override the tool description shown to the LLM")
	return cmd
}
