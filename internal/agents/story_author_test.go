package agents

import (
	"reflect"
	"strings"
	"testing"
)

func TestStoryAuthorPromptNonEmpty(t *testing.T) {
	a := storyAuthor()
	if len(a.SystemPrompt) < 200 {
		t.Errorf("storyAuthor().SystemPrompt is only %d bytes; expected >= 200",
			len(a.SystemPrompt))
	}
}

func TestStoryAuthorPromptHasNoTemplateTokens(t *testing.T) {
	a := storyAuthor()
	forbidden := []string{
		"{{SHADOW_DIR}}",
		"{{CURRENT_VIEW}}",
		"{{APP_PATH}}",
	}
	for _, tok := range forbidden {
		if strings.Contains(a.SystemPrompt, tok) {
			t.Errorf("story-author SystemPrompt contains forbidden token %q", tok)
		}
	}
}

// TestStoryAuthorPromptMentionsContextProtocol guards Phase A.6 + A.7:
// the agent prompt must teach the [context]/[user] preamble, tell the
// agent to set app_file from the context, AND describe trace_file as
// the session-history source the agent should Read. Future edits that
// drop any of these substrings break this test — the controller's
// preamble protocol and the always-on trace dump both depend on the
// agent understanding them.
func TestStoryAuthorPromptMentionsContextProtocol(t *testing.T) {
	a := storyAuthor()
	required := []string{
		"[context]",
		"app_file",
		"trace_file",
		"session history",
	}
	for _, sub := range required {
		if !strings.Contains(a.SystemPrompt, sub) {
			t.Errorf("storyAuthor().SystemPrompt missing required substring %q", sub)
		}
	}
}

func TestStoryAuthorToolsOrder(t *testing.T) {
	a := storyAuthor()
	want := []string{"Read", "Edit", "Write", "Bash", "Grep", "Glob"}
	if !reflect.DeepEqual(a.Tools, want) {
		t.Errorf("storyAuthor().Tools = %v, want %v", a.Tools, want)
	}
}
