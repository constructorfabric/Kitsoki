// from_harness.go implements the harness adapter.
//
// FromHarness wraps a harness.Harness as an Agent so that existing harness
// implementations (claude_cli, live, replay, recording) can serve as agent
// plugins without modification. This is the backward-compatibility bridge:
// agent.claude resolves to FromHarness(existingHarness) so cloak, oregon-trail,
// and bugfix keep running unchanged.
//
// MCP coupling: the harness.Harness interface returns mcp.CallToolParams whose
// Arguments field carries the LLM's tool-call payload. FromHarness marshals
// Arguments to JSON as the AskResponse.Submission. The MCP type dependency is
// entirely inside this file; all callers see only AskRequest / AskResponse.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/harness"
)

// harnessAgent wraps a harness.Harness as an Agent.
type harnessAgent struct {
	h harness.Harness
}

// FromHarness returns an Agent backed by h. Schema-bound calls use the optional
// harness.StructuredHarness capability so the caller's contract survives;
// schema-less compatibility calls still convert AskRequest to TurnInput and
// map the transition arguments back to AskResponse.
//
// The MCP-shape coupling is contained inside this adapter; outside callers see
// only AskRequest / AskResponse.
func FromHarness(h harness.Harness) Agent {
	return harnessAgent{h: h}
}

// Ask converts req to a TurnInput, calls the underlying harness, and maps the
// result to AskResponse. Error mapping:
//   - context deadline exceeded → AskError{Kind: "deadline_exceeded"}
//   - harness.ClarifyResponse (LLM did not call the expected tool) → AskError{Kind: "schema_invalid"}
//   - any other error → AskError{Kind: "plugin_crash"}
func (o harnessAgent) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	if len(req.SchemaJSON) > 0 {
		structured, ok := o.h.(harness.StructuredHarness)
		if !ok {
			return AskResponse{}, &AskError{
				Kind:   "plugin_unsupported",
				Detail: "harness adapter: underlying harness does not support arbitrary structured output",
			}
		}
		submission, err := structured.RunStructured(ctx, harness.StructuredInput{
			SessionID:  req.SessionID,
			TurnNumber: req.TurnNumber,
			StatePath:  req.StatePath,
			Prompt:     req.PromptText,
			SchemaJSON: req.SchemaJSON,
		})
		if err != nil {
			return AskResponse{}, mapHarnessError(err)
		}
		return AskResponse{Submission: submission}, nil
	}

	in := harness.TurnInput{
		SessionID:  req.SessionID,
		TurnNumber: req.TurnNumber,
		StatePath:  req.StatePath,
		// PromptText is the fully rendered agent prompt; map it to UserText so
		// the harness passes it to the LLM verbatim.
		UserText:     req.PromptText,
		World:        req.World,
		SystemPrompt: "", // not surfaced on AskRequest; harness uses its own system prompt
	}

	params, err := o.h.RunTurn(ctx, in)
	if err != nil {
		return AskResponse{}, mapHarnessError(err)
	}

	submission, marshalErr := marshalArguments(params)
	if marshalErr != nil {
		return AskResponse{}, &AskError{
			Kind:       "plugin_crash",
			Underlying: marshalErr,
			Detail:     fmt.Sprintf("harness adapter: marshal CallToolParams.Arguments: %v", marshalErr),
		}
	}

	return AskResponse{
		Submission: submission,
		Meta:       nil, // harness does not expose token/cost metadata
	}, nil
}

// Close closes the underlying harness.
func (o harnessAgent) Close() error {
	return o.h.Close()
}

// mapHarnessError converts a harness error to the appropriate AskError Kind.
func mapHarnessError(err error) *AskError {
	if err == nil {
		return nil
	}

	// Context deadline / cancellation.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &AskError{
			Kind:       "deadline_exceeded",
			Underlying: err,
			Detail:     err.Error(),
		}
	}

	// harness.ClarifyResponse: the LLM responded but did not call the expected
	// tool (MCP validator's submit). This is semantically a schema-invalid case
	// from the agent contract's perspective — the plugin did not produce a
	// schema-shaped Submission.
	var cr *harness.ClarifyResponse
	if errors.As(err, &cr) {
		detail := "harness: LLM did not call the expected tool"
		if cr.Message != "" {
			detail = cr.Message
		}
		return &AskError{
			Kind:       "schema_invalid",
			Underlying: err,
			Detail:     detail,
		}
	}

	// Any other harness error is treated as a plugin crash.
	return &AskError{
		Kind:       "plugin_crash",
		Underlying: err,
		Detail:     err.Error(),
	}
}

// marshalArguments converts mcp.CallToolParams.Arguments to a json.RawMessage.
// Arguments is already a map[string]any; marshalling it produces valid JSON.
func marshalArguments(params mcp.CallToolParams) (json.RawMessage, error) {
	if params.Arguments == nil {
		return json.RawMessage("null"), nil
	}
	b, err := json.Marshal(params.Arguments)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}
