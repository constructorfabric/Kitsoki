package harness

import (
	"context"
	"encoding/json"

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
	// SystemPrompt is an extra rendered prompt fragment the orchestrator may
	// pass through (e.g. Agent-Room surfaces); harnesses append it verbatim
	// to the dynamic suffix.
	SystemPrompt string `json:"system_prompt,omitempty"`
	// RecentTurns is an ordered tail (oldest → newest) of the most recent N
	// completed turns in this session, included so the LLM can resolve
	// back-references like "what I just said" or "that thing I tried before".
	// Empty on turn 1. Populated by the orchestrator from the session event
	// log; harnesses are expected to render this into the system prompt as
	// a compact "Recent conversation" block.
	RecentTurns []TurnSummary `json:"recent_turns,omitempty"`
}

// TurnSummary is a compact view of one prior turn for harness context. It
// captures just enough to give the LLM back-reference grounding without
// inflating the prompt: the user utterance (for anaphora resolution), the
// routed intent (so "what I just did" is decidable), the state the user
// landed in after the turn (the structural anchor), and whether the turn
// was rejected (so "the thing I tried that failed" is unambiguous).
//
// View text is intentionally omitted: it is often the largest piece of a
// turn record and is rarely needed for back-reference resolution. If we
// later want full-fidelity replays we can either add a View field with a
// truncation cap or have the harness fetch on demand.
type TurnSummary struct {
	// Turn is the monotonic turn number this summary represents.
	Turn app.TurnNumber `json:"turn"`
	// UserText is the raw user utterance from this prior turn.
	UserText string `json:"user_text"`
	// Intent is the intent name the harness routed the prior turn to.
	// Empty when the turn was rejected before routing (e.g. UNKNOWN_INTENT).
	Intent string `json:"intent,omitempty"`
	// Slots is the slot bag the harness passed for this prior turn. Carried
	// so back-references like "yes — like I said before" or "same as last
	// time" can reach not just the verb but the *values* the user previously
	// supplied. Without this, the LLM sees "the prior intent was
	// propose_purchase" but cannot re-submit `items` + `total_cost` because
	// they're not in scope — Claude then asks the user to repeat themselves
	// and the harness errors out on a missing submit call.
	Slots map[string]any `json:"slots,omitempty"`
	// State is the active state path AFTER the prior turn completed (the
	// post-transition state on success, or the unchanged state on rejection).
	State app.StatePath `json:"state,omitempty"`
	// Rejected is true when the prior turn ended in ModeRejected. Lets the
	// LLM tell "what I asked for" from "what I succeeded in doing".
	Rejected bool `json:"rejected,omitempty"`
}

// Harness is the pluggable LLM runner: the routing tier that consults a
// language model after the deterministic and semantic tiers miss. Every
// implementation (Live, ClaudeCLI, Replay, Recording) satisfies this two-method
// contract so the orchestrator can swap backends without knowing which one it
// holds. Implementations are not required to be safe for concurrent RunTurn
// calls; the orchestrator serialises turns per session.
type Harness interface {
	// RunTurn pipes the user utterance and app context to the LLM; blocks until
	// the LLM makes a tool call or exits. The returned CallToolParams is validated
	// upstream by the MCP server, not here.
	RunTurn(ctx context.Context, in TurnInput) (mcp.CallToolParams, error)

	// Close releases any subprocess or network resources held by the harness.
	Close() error
}

// StructuredInput is a schema-bound, non-routing request handled by harnesses
// that can expose their underlying model as a general agent plugin. Unlike
// TurnInput, the supplied schema is the complete output contract; it must not
// be replaced with the app's transition schema.
type StructuredInput struct {
	SessionID  app.SessionID
	TurnNumber app.TurnNumber
	StatePath  app.StatePath
	Prompt     string
	SchemaJSON json.RawMessage
}

// StructuredHarness is the optional capability used by agent.FromHarness for
// arbitrary schema-bound agent calls. Keeping it separate from Harness leaves
// replay-only routing harnesses source-compatible while preventing the old,
// unsafe fallback of treating a contextual-router schema as a normal intent
// turn with an empty allowed-intent list.
type StructuredHarness interface {
	RunStructured(ctx context.Context, in StructuredInput) (json.RawMessage, error)
}

// ClarifyResponse is the error type returned by a harness when the LLM
// answered but did not call the tool the harness was waiting for — most
// commonly the MCP validator's `submit`. The LLM's free-form response is
// preserved on Message so the orchestrator can surface it to the user as
// a soft clarification ("the router needs more from you") rather than a
// red technical error ("LLM did not call mcp__kitsoki-validator__submit").
//
// Match with errors.As. The orchestrator looks for this specifically and
// translates the turn to ModeRejected with ErrorCode "LLM_CLARIFICATION"
// and ErrorMessage = Message.
type ClarifyResponse struct {
	// Message is the LLM's free-form text. Empty when the LLM exited
	// silently — in that case the orchestrator falls back to a generic
	// "the router didn't understand" hint.
	Message string
	// Underlying is the original technical error (preserved for the
	// trace); the user-facing surface uses Message.
	Underlying error
}

// Error implements the error interface. The string is the technical form
// (suitable for the trace log); the user-facing surface should read
// Message instead.
func (c *ClarifyResponse) Error() string {
	if c == nil {
		return "harness: nil ClarifyResponse"
	}
	if c.Underlying != nil {
		return c.Underlying.Error()
	}
	return "harness: LLM responded without calling the expected tool: " + c.Message
}

// Unwrap lets errors.Is / errors.As reach the underlying error.
func (c *ClarifyResponse) Unwrap() error {
	if c == nil {
		return nil
	}
	return c.Underlying
}
