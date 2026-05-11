// Package harness defines the Harness interface and its type dependencies.
// Three implementations are planned (§12.1): Live (anthropic-sdk-go or claude -p
// subprocess), Replay (YAML recording for Mode 2 tests), and Recording (wraps Live).
package harness

import (
	"context"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/world"

	// Blank import keeps anthropic-sdk-go in go.mod after tidy.
	_ "github.com/anthropics/anthropic-sdk-go"
)

// TurnInput is the structured input passed to Harness.RunTurn each turn.
// It bundles everything the LLM needs: user utterance, current state context,
// allowed intents, and the running conversation history.
type TurnInput struct {
	// SessionID identifies the current session.
	SessionID app.SessionID `json:"session_id"`
	// TurnNumber is the current turn counter.
	TurnNumber app.TurnNumber `json:"turn_number"`
	// UserText is the raw user utterance.
	UserText string `json:"user_text"`
	// StatePath is the current active state path.
	StatePath app.StatePath `json:"state_path"`
	// World is the current world snapshot, passed for system-prompt generation.
	World world.World `json:"world"`
	// AllowedIntents lists the intent names currently valid.
	AllowedIntents []string `json:"allowed_intents"`
	// SystemPrompt is the rendered system prompt (including app context and §7 surfaces).
	SystemPrompt string `json:"system_prompt,omitempty"`
}

// Harness is the pluggable LLM runner (§12.1).
// This is one of the five core interfaces from §12.1.
type Harness interface {
	// RunTurn pipes the user utterance and app context to the LLM; blocks until
	// the LLM makes a tool call or exits. The returned CallToolParams is validated
	// upstream by the MCP server, not here.
	RunTurn(ctx context.Context, in TurnInput) (mcp.CallToolParams, error)

	// Close releases any subprocess or network resources held by the harness.
	Close() error
}
