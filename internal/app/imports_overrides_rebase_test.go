package app

import (
	"path/filepath"
	"testing"
)

// TestRebaseWithMap_NestedTaskPaths pins that an imported child's
// host.agent.task paths rebase to the child story's directory:
//   - context.prompt / context.prompt_path (nested under with.context)
//   - acceptance.schema (nested under with.acceptance)
//
// Regression: acceptance.schema was NOT rebased, so the runtime joined the
// relative path against the PARENT app dir ($KITSOKI_APP_DIR) and failed with
// "schema ... not found" — silently swallowed by the room's on_error, leaving
// the brief unwritten. See rooms/proposal.yaml brief_distill.
func TestRebaseWithMap_NestedTaskPaths(t *testing.T) {
	childDir := "/repo/stories/dev-story"
	with := map[string]any{
		"agent":       "proposal_brief_writer",
		"working_dir": "{{ world.proposal_workspace }}", // template — left alone
		"acceptance": map[string]any{
			"schema": "schemas/brief-distill.json",
		},
		"context": map[string]any{
			"prompt": "prompts/proposal_brief_distill.md",
		},
	}

	rebaseWithMap(with, childDir)

	acc := with["acceptance"].(map[string]any)
	if got, want := acc["schema"].(string), filepath.Join(childDir, "schemas/brief-distill.json"); got != want {
		t.Errorf("acceptance.schema = %q, want %q", got, want)
	}
	ctx := with["context"].(map[string]any)
	if got, want := ctx["prompt"].(string), filepath.Join(childDir, "prompts/proposal_brief_distill.md"); got != want {
		t.Errorf("context.prompt = %q, want %q", got, want)
	}
	// Templated working_dir must be left untouched.
	if got := with["working_dir"].(string); got != "{{ world.proposal_workspace }}" {
		t.Errorf("working_dir rewritten unexpectedly: %q", got)
	}
}
