// migrate_agent_roundtrip_test.go — round-trip parse assertions for the
// migrate-agent codemod. Every rewritten YAML must parse without error
// with goccy/go-yaml. Also covers the off-by-one regression (1-indexed
// token.Position.Offset) that produced broken YAML for unquoted verbs.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

// mustRewriteAgentYAML is a test helper that writes src to a temp file,
// runs the migrate-agent codemod against it, reads back the result, and
// returns both the rewritten bytes and the temp path.
func mustRewriteAgentYAML(t *testing.T, src []byte) (rewritten []byte, path string) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(path, src, 0644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cmd := newRootCmd()
	var stdout strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"migrate-agent", path})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("migrate-agent execute: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten: %v", err)
	}
	return got, path
}

// assertParseable asserts that b is valid YAML.
func assertParseable(t *testing.T, b []byte) {
	t.Helper()
	var doc any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("rewritten YAML does not parse: %v\n--- rewritten ---\n%s", err, b)
	}
}

// TestMigrateAgent_RoundTrip_UnquotedVerb is the regression test for the
// off-by-one bug: goccy token.Position.Offset is 1-indexed, so the codemod
// must subtract 1 before using it as a byte index. Before the fix the
// rewrite produced broken YAML (double first-char, lost newline).
func TestMigrateAgent_RoundTrip_UnquotedVerb(t *testing.T) {
	t.Parallel()
	const src = `app:
  id: test
states:
  s:
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with: {prompt: x.md}
`
	rewritten, _ := mustRewriteAgentYAML(t, []byte(src))

	// Must contain the new verb, not the old one.
	if !strings.Contains(string(rewritten), "host.agent.ask") {
		t.Fatalf("expected host.agent.ask in rewritten YAML:\n%s", rewritten)
	}
	if strings.Contains(string(rewritten), "host.agent.ask_with_mcp") {
		t.Fatalf("old verb must be replaced:\n%s", rewritten)
	}
	// Must be parseable — this is what the off-by-one bug broke.
	assertParseable(t, rewritten)
}

// TestMigrateAgent_RoundTrip_SingleQuotedVerb covers single-quoted verbs.
func TestMigrateAgent_RoundTrip_SingleQuotedVerb(t *testing.T) {
	t.Parallel()
	const src = `app:
  id: test
states:
  s:
    on_enter:
      - invoke: 'host.agent.ask_with_mcp'
        with: {prompt: x.md}
`
	rewritten, _ := mustRewriteAgentYAML(t, []byte(src))
	assertParseable(t, rewritten)
	if strings.Contains(string(rewritten), "ask_with_mcp") {
		t.Fatalf("old verb must be replaced:\n%s", rewritten)
	}
}

// TestMigrateAgent_RoundTrip_DoubleQuotedVerb covers double-quoted verbs.
func TestMigrateAgent_RoundTrip_DoubleQuotedVerb(t *testing.T) {
	t.Parallel()
	const src = `app:
  id: test
states:
  s:
    on_enter:
      - invoke: "host.agent.ask_with_mcp"
        with: {prompt: x.md}
`
	rewritten, _ := mustRewriteAgentYAML(t, []byte(src))
	assertParseable(t, rewritten)
	if strings.Contains(string(rewritten), "ask_with_mcp") {
		t.Fatalf("old verb must be replaced:\n%s", rewritten)
	}
}

// TestMigrateAgent_RoundTrip_TrailingWhitespace covers verbs followed by
// trailing whitespace before the newline (e.g. editor artefacts).
func TestMigrateAgent_RoundTrip_TrailingWhitespace(t *testing.T) {
	t.Parallel()
	// Trailing space after the verb value — YAML ignores it; the token value
	// is still the verb string without the space.
	src := []byte("app:\n  id: test\nstates:\n  s:\n    on_enter:\n      - invoke: host.agent.ask_with_mcp   \n        with: {prompt: x.md}\n")
	rewritten, _ := mustRewriteAgentYAML(t, src)
	assertParseable(t, rewritten)
}

// TestMigrateAgent_RoundTrip_WithComment covers a verb on a line that has
// a neighbouring comment in the mapping.
func TestMigrateAgent_RoundTrip_WithComment(t *testing.T) {
	t.Parallel()
	const src = `app:
  id: test
states:
  s:
    # ask_with_mcp usage — will be migrated
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with:
          schema: schemas/v.json
          prompt: prompts/p.md
`
	rewritten, _ := mustRewriteAgentYAML(t, []byte(src))
	assertParseable(t, rewritten)
	if strings.Contains(string(rewritten), "ask_with_mcp") {
		// The comment may still contain the old name — only the invoke: value
		// must be replaced.
		lines := strings.Split(string(rewritten), "\n")
		for _, l := range lines {
			if strings.Contains(l, "invoke:") && strings.Contains(l, "ask_with_mcp") {
				t.Fatalf("invoke: line still contains ask_with_mcp:\n%s", rewritten)
			}
		}
	}
}

// TestMigrateAgent_RoundTrip_MultipleVerbs covers a file with more than one
// verb site. Offset accounting must remain correct after each rewrite because
// the byte-surgery loop applies edits in descending order.
func TestMigrateAgent_RoundTrip_MultipleVerbs(t *testing.T) {
	t.Parallel()
	const src = `app:
  id: test
states:
  a:
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with: {prompt: a.md}
  b:
    on_enter:
      - invoke: host.agent.ask_with_mcp
        with:
          schema: schemas/b.json
          prompt: b.md
  c:
    on_enter:
      - invoke: host.agent.talk
        with: {question: hello}
`
	rewritten, _ := mustRewriteAgentYAML(t, []byte(src))
	assertParseable(t, rewritten)
	if strings.Contains(string(rewritten), "ask_with_mcp") {
		t.Fatalf("all ask_with_mcp verbs must be replaced:\n%s", rewritten)
	}
	if strings.Contains(string(rewritten), "host.agent.talk") {
		t.Fatalf("talk verb must be replaced:\n%s", rewritten)
	}
	// Expect: a→ask, b→decide, c→converse
	if !strings.Contains(string(rewritten), "host.agent.ask") {
		t.Fatalf("expected host.agent.ask for state a:\n%s", rewritten)
	}
	if !strings.Contains(string(rewritten), "host.agent.decide") {
		t.Fatalf("expected host.agent.decide for state b:\n%s", rewritten)
	}
	if !strings.Contains(string(rewritten), "host.agent.converse") {
		t.Fatalf("expected host.agent.converse for state c:\n%s", rewritten)
	}
}

// ── round-trip assertions backfilled into every TestMigrateAgentFile_* ──

// TestMigrateAgentFile_TalkToConverse_RoundTrip asserts yamlWithTalk parses
// after rewrite.
func TestMigrateAgentFile_TalkToConverse_RoundTrip(t *testing.T) {
	t.Parallel()
	rewritten, _ := mustRewriteAgentYAML(t, []byte(yamlWithTalk))
	assertParseable(t, rewritten)
}

// TestMigrateAgentFile_SchemaToDecide_RoundTrip asserts yamlWithSchema parses
// after rewrite.
func TestMigrateAgentFile_SchemaToDecide_RoundTrip(t *testing.T) {
	t.Parallel()
	rewritten, _ := mustRewriteAgentYAML(t, []byte(yamlWithSchema))
	assertParseable(t, rewritten)
}

// TestMigrateAgentFile_NoSchemaToAsk_RoundTrip asserts yamlWithNoSchema parses
// after rewrite.
func TestMigrateAgentFile_NoSchemaToAsk_RoundTrip(t *testing.T) {
	t.Parallel()
	rewritten, _ := mustRewriteAgentYAML(t, []byte(yamlWithNoSchema))
	assertParseable(t, rewritten)
}

// TestMigrateAgentFile_ChatIDToConverse_RoundTrip asserts yamlWithChatID
// parses after rewrite.
func TestMigrateAgentFile_ChatIDToConverse_RoundTrip(t *testing.T) {
	t.Parallel()
	rewritten, _ := mustRewriteAgentYAML(t, []byte(yamlWithChatID))
	assertParseable(t, rewritten)
}

// TestMigrateAgentFile_MutationToTask_RoundTrip asserts yamlWithMutation
// parses after rewrite.
func TestMigrateAgentFile_MutationToTask_RoundTrip(t *testing.T) {
	t.Parallel()
	rewritten, _ := mustRewriteAgentYAML(t, []byte(yamlWithMutation))
	assertParseable(t, rewritten)
}

// TestMigrateAgentFile_MutationFull_RoundTrip asserts yamlWithMutationFull
// parses after the with: block restructure.
func TestMigrateAgentFile_MutationFull_RoundTrip(t *testing.T) {
	t.Parallel()
	rewritten, _ := mustRewriteAgentYAML(t, []byte(yamlWithMutationFull))
	assertParseable(t, rewritten)
}

// TestMigrateAgent_BugfixRoomShape_Implementing verifies that the codemod,
// when given a pre-migration implementing-room fixture (ask_with_mcp with agent
// + mutation tools + schema + prompt + working_dir + args), produces the same
// structural shape as the hand-corrected stories/bugfix/rooms/implementing.yaml:
// agent and working_dir at the top level of with:, schema under acceptance:, and
// prompt+args under context:.
func TestMigrateAgent_BugfixRoomShape_Implementing(t *testing.T) {
	t.Parallel()
	// This fixture mirrors the pre-migration state of the implementing room
	// (as it would have looked before Phase 8's manual verb change), but with
	// the tools: block that causes classification as task.
	const preMigration = `states:
  implementing:
    on_enter:
      - when: "world.workdir != ''"
        invoke: host.agent.ask_with_mcp
        with:
          agent: implementer
          prompt: prompts/implementing_executing.md
          schema: schemas/implementing_artifact.json
          working_dir: "{{ world.workdir }}"
          args:
            ticket_id:        "{{ world.ticket_id }}"
            ticket_title:     "{{ world.ticket_title }}"
            workdir:          "{{ world.workdir }}"
            fix_description:  "{{ world.propose_fix_artifact.fix_description }}"
            root_cause:       "{{ world.propose_fix_artifact.root_cause }}"
            affected_files:   "{{ world.propose_fix_artifact.affected_files }}"
            refine_feedback:  "{{ world.refine_feedback }}"
            cycle:            "{{ world.cycle }}"
          tools:
            - Edit
            - Bash
        bind:
          implement_artifact: submitted
        on_error: idle
`
	rewritten, _ := mustRewriteAgentYAML(t, []byte(preMigration))
	assertParseable(t, rewritten)
	s := string(rewritten)

	if !strings.Contains(s, "invoke: host.agent.task") {
		t.Fatalf("expected host.agent.task:\n%s", s)
	}
	if strings.Contains(s, "ask_with_mcp") {
		t.Fatalf("old verb must be replaced:\n%s", s)
	}

	// acceptance: must contain schema:
	if !strings.Contains(s, "acceptance:") {
		t.Fatalf("expected acceptance: sub-block:\n%s", s)
	}
	if !strings.Contains(s, "schema: schemas/implementing_artifact.json") {
		t.Fatalf("schema must be under acceptance::\n%s", s)
	}

	// context: must contain prompt: and args:
	if !strings.Contains(s, "context:") {
		t.Fatalf("expected context: sub-block:\n%s", s)
	}
	if !strings.Contains(s, "prompt: prompts/implementing_executing.md") {
		t.Fatalf("prompt must be under context::\n%s", s)
	}
	if !strings.Contains(s, "ticket_id:") {
		t.Fatalf("args must be preserved under context.args::\n%s", s)
	}

	// agent: and working_dir: must stay at the top level of with:.
	if !strings.Contains(s, "agent: implementer") {
		t.Fatalf("agent must be at top of with::\n%s", s)
	}
	if !strings.Contains(s, `working_dir: "{{ world.workdir }}"`) {
		t.Fatalf("working_dir must be at top of with::\n%s", s)
	}

	// bind: and on_error: must survive outside with:.
	if !strings.Contains(s, "bind:") {
		t.Fatalf("bind: must be preserved:\n%s", s)
	}
	if !strings.Contains(s, "on_error: idle") {
		t.Fatalf("on_error must be preserved:\n%s", s)
	}
}
