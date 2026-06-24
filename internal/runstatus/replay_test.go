package runstatus

import (
	"encoding/json"
	"errors"
	"testing"

	"kitsoki/internal/store"
)

// makeAgentCalledEvent builds a minimal agent.call.start store.Event for
// testing. payload is marshalled to JSON and set as the event Payload; callID
// is set on both the Event.CallID field and inside payload["call_id"] so the
// extractor can match by either path.
func makeAgentCalledEvent(callID string, payload map[string]any) store.Event {
	payload["call_id"] = callID
	raw, _ := json.Marshal(payload)
	return store.Event{
		Kind:    store.AgentCalled,
		CallID:  callID,
		Payload: json.RawMessage(raw),
	}
}

func TestExtractReplayableCall(t *testing.T) {
	t.Parallel()
	const wantCallID = "abc123"
	events := []store.Event{
		makeAgentCalledEvent(wantCallID, map[string]any{
			"verb":   "decide",
			"prompt": "Should we proceed?",
			"agent":  "agent.claude",
			"model":  "claude-3-5-sonnet-20241022",
		}),
	}

	rc, err := ExtractReplayableCall(events, wantCallID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rc.CallID != wantCallID {
		t.Errorf("CallID = %q; want %q", rc.CallID, wantCallID)
	}
	if rc.Verb != "decide" {
		t.Errorf("Verb = %q; want %q", rc.Verb, "decide")
	}
	if rc.Prompt != "Should we proceed?" {
		t.Errorf("Prompt = %q; want %q", rc.Prompt, "Should we proceed?")
	}
	if rc.Agent != "agent.claude" {
		t.Errorf("Agent = %q; want %q", rc.Agent, "agent.claude")
	}
	if rc.Model != "claude-3-5-sonnet-20241022" {
		t.Errorf("Model = %q; want %q", rc.Model, "claude-3-5-sonnet-20241022")
	}
}

func TestExtractReplayableCall_promptFile(t *testing.T) {
	t.Parallel()
	const wantCallID = "def456"
	events := []store.Event{
		makeAgentCalledEvent(wantCallID, map[string]any{
			"verb":        "ask",
			"prompt_file": "/tmp/kitsoki/prompts/abc.txt",
		}),
	}
	rc, err := ExtractReplayableCall(events, wantCallID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rc.PromptFile != "/tmp/kitsoki/prompts/abc.txt" {
		t.Errorf("PromptFile = %q; want non-empty", rc.PromptFile)
	}
	if rc.Prompt != "" {
		t.Errorf("Prompt should be empty when only prompt_file is set, got %q", rc.Prompt)
	}
}

func TestExtractReplayableCall_unsupported_task(t *testing.T) {
	t.Parallel()
	events := []store.Event{
		makeAgentCalledEvent("task1", map[string]any{
			"verb":   "task",
			"prompt": "do something with side effects",
		}),
	}
	_, err := ExtractReplayableCall(events, "task1")
	if !errors.Is(err, ErrNotReplayable) {
		t.Errorf("expected ErrNotReplayable for verb=task, got %v", err)
	}
}

func TestExtractReplayableCall_unsupported_converse(t *testing.T) {
	t.Parallel()
	events := []store.Event{
		makeAgentCalledEvent("conv1", map[string]any{
			"verb":   "converse",
			"prompt": "let's chat",
		}),
	}
	_, err := ExtractReplayableCall(events, "conv1")
	if !errors.Is(err, ErrNotReplayable) {
		t.Errorf("expected ErrNotReplayable for verb=converse, got %v", err)
	}
}

func TestExtractReplayableCall_missing_prompt(t *testing.T) {
	t.Parallel()
	events := []store.Event{
		makeAgentCalledEvent("noprompt", map[string]any{
			"verb": "decide",
			// no prompt, no prompt_file
		}),
	}
	_, err := ExtractReplayableCall(events, "noprompt")
	if !errors.Is(err, ErrNotReplayable) {
		t.Errorf("expected ErrNotReplayable when prompt is absent, got %v", err)
	}
}

func TestExtractReplayableCall_missing_event(t *testing.T) {
	t.Parallel()
	// Empty events slice — no agent.call.start present.
	_, err := ExtractReplayableCall(nil, "ghost")
	if !errors.Is(err, ErrNotReplayable) {
		t.Errorf("expected ErrNotReplayable for empty events, got %v", err)
	}
}

func TestExtractReplayableCall_wrong_callid(t *testing.T) {
	t.Parallel()
	events := []store.Event{
		makeAgentCalledEvent("abc", map[string]any{
			"verb":   "decide",
			"prompt": "are you sure?",
		}),
	}
	_, err := ExtractReplayableCall(events, "xyz")
	if !errors.Is(err, ErrNotReplayable) {
		t.Errorf("expected ErrNotReplayable for wrong callID, got %v", err)
	}
}
