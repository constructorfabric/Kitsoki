package runstatus

import (
	"encoding/json"
	"errors"

	"kitsoki/internal/store"
)

// ReplayableCall holds the inputs needed to re-dispatch one agent call.
// All fields are extracted from the agent.call.start event payload in the
// trace. Prompt is guaranteed non-empty on a valid ReplayableCall — if the
// trace carries only a prompt_file reference, PromptFile is set and the
// caller is responsible for resolving the file content.
type ReplayableCall struct {
	// CallID is the deterministic agent call identifier (matched by call_id on
	// agent.call.start / agent.call.complete pairs).
	CallID string
	// Verb is the agent verb: decide | ask | extract.
	// task and converse are not supported in v1 (side effects require sandbox).
	Verb string
	// Prompt is the resolved prompt text (from attrs.prompt on the call.start
	// event). Empty when only PromptFile is set.
	Prompt string
	// PromptFile is the path to an off-loaded prompt file (from attrs.prompt_file).
	// Set when the prompt was too large to embed inline and was stored in a
	// sidecar file instead.
	PromptFile string
	// Schema is the schema JSON string or path (from attrs.schema or attrs.input),
	// optional — not all verbs have a schema.
	Schema string
	// Agent is the agent agent name (from attrs.agent), optional.
	Agent string
	// Model is the model name (from attrs.model), optional.
	Model string
}

// ErrNotReplayable is returned when the trace lacks the required fields for
// replay: either the verb is unsupported (task/converse have side effects and
// require sandbox support not yet shipped) or the prompt reference is missing.
var ErrNotReplayable = errors.New("call is not replayable: missing prompt or unsupported verb")

// unsupportedVerbs are the agent verbs excluded from v1 replay because they
// can have side effects. task may invoke tools with file-system or network
// access; converse drives multi-turn interactions. Both require the
// task-fs-sandbox slice before replay is safe.
var unsupportedVerbs = map[string]bool{
	"task":     true,
	"converse": true,
}

// ExtractReplayableCall scans events for the agent.call.start event whose
// call_id matches callID and returns a ReplayableCall populated from its
// payload attrs.
//
// Returns ErrNotReplayable when:
//   - no agent.call.start event with the given callID exists in events
//   - the resolved verb is "task" or "converse" (side-effect verbs, v1 gate)
//   - both prompt and prompt_file are absent from the event payload
func ExtractReplayableCall(events []store.Event, callID string) (ReplayableCall, error) {
	for _, ev := range events {
		if ev.Kind != store.AgentCalled {
			continue
		}
		if ev.CallID != callID {
			continue
		}
		// Found the matching agent.call.start event. Decode its payload.
		var payload map[string]any
		if len(ev.Payload) > 0 {
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				return ReplayableCall{}, ErrNotReplayable
			}
		}

		verb, _ := payload["verb"].(string)
		if unsupportedVerbs[verb] {
			return ReplayableCall{}, ErrNotReplayable
		}

		prompt, _ := payload["prompt"].(string)
		promptFile, _ := payload["prompt_file"].(string)

		// A replayable call requires a prompt reference — either inline text or
		// a file path. Without it reconstruction would fabricate a prompt, which
		// the proposal explicitly prohibits.
		if prompt == "" && promptFile == "" {
			return ReplayableCall{}, ErrNotReplayable
		}

		agent, _ := payload["agent"].(string)
		model, _ := payload["model"].(string)

		// Schema: try the input.schema path first (structured), then a direct
		// schema field if present.
		schema := ""
		if input, ok := payload["input"].(map[string]any); ok {
			if sp, ok := input["schema_path"].(string); ok {
				schema = sp
			} else if s, ok := input["schema"].(string); ok {
				schema = s
			}
		}
		if schema == "" {
			if s, ok := payload["schema"].(string); ok {
				schema = s
			}
		}

		return ReplayableCall{
			CallID:     callID,
			Verb:       verb,
			Prompt:     prompt,
			PromptFile: promptFile,
			Schema:     schema,
			Agent:      agent,
			Model:      model,
		}, nil
	}

	// No matching event found — treat as not replayable.
	return ReplayableCall{}, ErrNotReplayable
}
