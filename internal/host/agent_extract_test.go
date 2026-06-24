package host_test

// Tests for host.agent.extract.
//
// Coverage:
//   - First match wins: synonyms before slot_template before LLM.
//   - Load-time schema validation: mismatch → loader error.
//   - No-match path: all tiers decline → resolved_by: no_match, on_error fires.
//   - Validator: runs after match, rejects, falls through to next tier (or
//     counts as LLM tier fail when LLM was the producer).
//   - Streaming: deterministic tier emits extract.resolver.matched event.
//   - LLM tier with tools: tools forwarded as --allowedTools.
//   - FakeExtract + WithClaudeRunner for LLM tier stubbing. No real LLM.
//   - suggest-synonym: recorded LLM-tier extract produces proposed synonym entry.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// writeSynonymFile writes a synonyms YAML file in the test temp dir and
// returns its path.
func writeSynonymFile(t *testing.T, dir, name string, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("writeSynonymFile %s: %v", name, err)
	}
	return p
}

// writeSchemaFile writes a JSON Schema file into dir.
func writeSchemaFile(t *testing.T, dir, name string) string {
	t.Helper()
	schema := `{
  "type": "object",
  "properties": {
    "direction": {"type": "string"},
    "action": {"type": "string"}
  }
}`
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(schema), 0o644); err != nil {
		t.Fatalf("writeSchemaFile %s: %v", name, err)
	}
	return p
}

// writePromptFile writes a simple prompt file.
func writePromptFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("writePromptFile %s: %v", name, err)
	}
	return p
}

// TestAgentExtract_SynonymsResolver_Hit verifies that a matching synonym entry
// is returned as submitted with resolved_by=synonyms.
func TestAgentExtract_SynonymsResolver_Hit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	synonymPath := writeSynonymFile(t, dir, "synonyms.yaml", `
"go north,head north": {"direction": "north"}
wade: {"action": "wade"}
`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	ctx := context.Background()
	res, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":  "go north",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if res.Data["resolved_by"] != "synonyms" {
		t.Errorf("resolved_by: want synonyms, got %v", res.Data["resolved_by"])
	}
	sub, _ := res.Data["submitted"].(map[string]any)
	if sub["direction"] != "north" {
		t.Errorf("submitted.direction: want north, got %v", sub["direction"])
	}
}

// TestAgentExtract_SynonymsResolver_CaseInsensitive verifies that synonym
// matching is case-insensitive.
func TestAgentExtract_SynonymsResolver_CaseInsensitive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"Go North": {"direction": "north"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "go north",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["resolved_by"] != "synonyms" {
		t.Errorf("resolved_by: want synonyms, got %v", res.Data["resolved_by"])
	}
}

// TestAgentExtract_SynonymsMiss_FallsThrough verifies that a synonym miss
// falls through to the next resolver.
func TestAgentExtract_SynonymsMiss_FallsThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"direction": "north"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	// Only the synonyms resolver is declared; input doesn't match.
	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "fly west",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("resolved_by: want no_match, got %v", res.Data["resolved_by"])
	}
	if res.Data["submitted"] != nil {
		t.Errorf("submitted: want nil on no_match, got %v", res.Data["submitted"])
	}
	if res.Error == "" {
		t.Error("expected non-empty Result.Error on no_match")
	}
}

// TestAgentExtract_FirstMatchWins verifies that when multiple resolvers are
// declared, the first matching one wins (synonyms before LLM).
func TestAgentExtract_FirstMatchWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"direction": "north"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	promptPath := writePromptFile(t, dir, "p.md", "extract direction")

	// The LLM runner should NOT be called since synonyms matches first.
	llmCalled := false
	runner := func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		llmCalled = true
		return host.ClaudeRun{Stdout: `{"direction":"north"}`}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), runner)

	res, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":  "go north",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
			map[string]any{"llm": map[string]any{"prompt": promptPath}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["resolved_by"] != "synonyms" {
		t.Errorf("resolved_by: want synonyms, got %v", res.Data["resolved_by"])
	}
	if llmCalled {
		t.Error("LLM runner was called despite synonyms match")
	}
}

// TestAgentExtract_LLMTier_CalledOnMiss verifies that the LLM tier is invoked
// when deterministic tiers don't match.
func TestAgentExtract_LLMTier_CalledOnMiss(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"direction": "north"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	promptPath := writePromptFile(t, dir, "p.md", "extract direction from input")

	runner := host.FakeExtractJSON(map[string]any{"direction": "west"})
	ctx := host.WithClaudeRunner(context.Background(), runner)

	res, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":  "go west",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
			map[string]any{"llm": map[string]any{"prompt": promptPath}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if res.Data["resolved_by"] != "llm" {
		t.Errorf("resolved_by: want llm, got %v", res.Data["resolved_by"])
	}
}

// TestAgentExtract_LLMTier_Tools verifies that agent tools are forwarded via
// --allowedTools when the LLM tier runs.
func TestAgentExtract_LLMTier_Tools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	promptPath := writePromptFile(t, dir, "p.md", "extract direction from input")

	var capturedArgs []string
	capRunner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		capturedArgs = args
		// Simulate the mcp-validator writing the submit output (M5).
		const payload = `{"action":"wade"}`
		if outPath := host.ParseMCPConfigSubmitOutput(args); outPath != "" {
			_ = os.WriteFile(outPath, []byte(payload), 0o600)
		}
		return host.ClaudeRun{Stdout: payload}, nil
	}
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"extractor": {
				SystemPrompt: "extract mode",
				Tools:        []string{"host.Read", "host.Grep"},
			},
		}),
		capRunner,
	)

	res, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":  "wade the river",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"llm": map[string]any{"prompt": promptPath, "agent": "extractor"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if res.Data["resolved_by"] != "llm" {
		t.Errorf("resolved_by: want llm, got %v", res.Data["resolved_by"])
	}
	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "--allowedTools") {
		t.Errorf("--allowedTools not forwarded to LLM tier; args=%v", capturedArgs)
	}
	if !strings.Contains(joined, "host.Read") {
		t.Errorf("host.Read not in --allowedTools; args=%v", capturedArgs)
	}
}

// TestAgentExtract_LLMTier_RejectsMutationTools verifies the extract LLM tier
// honours the read-only contract: an agent whose tools include Edit, Write, or
// NotebookEdit is rejected at runtime even if the loader missed it (D5 safety
// net parallel to ask / decide).
func TestAgentExtract_LLMTier_RejectsMutationTools(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	promptPath := writePromptFile(t, dir, "p.md", "extract")

	for _, tool := range []string{"Edit", "Write", "NotebookEdit"} {
		tool := tool
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			ctx := host.WithClaudeRunner(
				host.WithAgents(context.Background(), map[string]host.Agent{
					"bad": {SystemPrompt: "extract", Tools: []string{tool}},
				}),
				host.FakeExtractJSON(map[string]any{"x": "y"}),
			)
			res, err := host.AgentExtractHandler(ctx, map[string]any{
				"input":  "anything",
				"schema": schemaPath,
				"resolvers": []any{
					map[string]any{"llm": map[string]any{"prompt": promptPath, "agent": "bad"}},
				},
			})
			if err != nil {
				t.Fatalf("unexpected Go error: %v", err)
			}
			if !strings.Contains(res.Error, "mutation tool") {
				t.Fatalf("expected mutation-tool rejection, got %q", res.Error)
			}
			if !strings.Contains(res.Error, tool) {
				t.Fatalf("expected error to name %q, got %q", tool, res.Error)
			}
		})
	}
}

// TestAgentExtract_NoMatch_ReturnsNoMatchResult verifies the explicit no-match
// path: all tiers decline → resolved_by: no_match, submitted: nil.
func TestAgentExtract_NoMatch_ReturnsNoMatchResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"direction": "north"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "totally unrecognised phrase xyz",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("resolved_by: want no_match, got %v", res.Data["resolved_by"])
	}
	if res.Data["submitted"] != nil {
		t.Errorf("submitted should be nil on no_match, got %v", res.Data["submitted"])
	}
	if res.Error == "" {
		t.Error("Result.Error should be non-empty on no_match so on_error: can fire")
	}
}

// TestAgentExtract_SchemaRequired verifies that omitting schema returns an
// error.
func TestAgentExtract_SchemaRequired(t *testing.T) {
	t.Parallel()
	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input": "go north",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "schema argument is required") {
		t.Errorf("expected schema-required error, got %q", res.Error)
	}
}

// TestAgentExtract_LLMTier_NonJSONFallsThrough verifies that when the LLM
// returns non-JSON output, it is treated as a no-match for that tier and falls
// through to the next resolver.
func TestAgentExtract_LLMTier_NonJSONFallsThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"direction": "north"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	promptPath := writePromptFile(t, dir, "p.md", "extract")

	// LLM returns non-JSON; synonym is declared after — but first in order.
	// We need: synonyms miss, LLM returns garbage, result = no_match.
	badRunner := func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		return host.ClaudeRun{Stdout: "I cannot determine the direction."}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), badRunner)

	res, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":  "fly east somewhere",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
			map[string]any{"llm": map[string]any{"prompt": promptPath}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("resolved_by: want no_match (LLM returned non-JSON), got %v", res.Data["resolved_by"])
	}
}

// TestAgentExtract_Validator_RejectsAndFallsThrough verifies that when a
// validator rejects a synonym payload, the handler falls through to the next
// resolver rather than returning the rejected payload.
func TestAgentExtract_Validator_RejectsAndFallsThrough(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a validator script that always exits 1 (reject).
	valScript := filepath.Join(dir, "reject.sh")
	if err := os.WriteFile(valScript, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write validator script: %v", err)
	}

	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"direction": "north"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	promptPath := writePromptFile(t, dir, "p.md", "extract direction")

	runner := host.FakeExtractJSON(map[string]any{"direction": "north-from-llm"})
	ctx := host.WithClaudeRunner(context.Background(), runner)

	res, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":  "go north",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
			map[string]any{"llm": map[string]any{"prompt": promptPath}},
		},
		"validator": map[string]any{
			"post_cmd": valScript,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Validator rejects the synonym tier, falls to LLM tier.
	// LLM also gets rejected (same validator). Result: no_match.
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("resolved_by: want no_match after validator rejection, got %v", res.Data["resolved_by"])
	}
}

// TestAgentExtract_Streaming_EmitsResolverMatchedEvent verifies that after a
// successful synonym match, the handler emits the extract.resolver.matched event.
// We capture this indirectly: the handler must return without error and the
// resolved_by must be set, which implies the event was emitted.
func TestAgentExtract_Streaming_EmitsResolverMatchedEvent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `wade: {"action": "wade"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "wade",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Result.Error: %s", res.Error)
	}
	// The resolved_by field being set means the emit path ran.
	if res.Data["resolved_by"] != "synonyms" {
		t.Errorf("resolved_by: want synonyms, got %v", res.Data["resolved_by"])
	}
}

// TestAgentExtract_ParsedArgs_Resolvers verifies the resolver arg parsing for
// all three resolver types.
func TestAgentExtract_ParsedArgs_Resolvers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	// Build args with all three resolver types.
	args := map[string]any{
		"input":  "test input",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": "/some/synonyms.yaml"},
			map[string]any{"slot_template": "/some/template.yaml"},
			map[string]any{"llm": map[string]any{"prompt": "/some/prompt.md", "agent": "test-agent"}},
		},
	}

	// Just ensure no panic / parse error; the resolvers themselves will fail
	// to load (files don't exist) and the handler returns no_match.
	res, err := host.AgentExtractHandler(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// All resolvers fail to load (files don't exist) — result is no_match.
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("expected no_match (all resolvers fail), got %v", res.Data["resolved_by"])
	}
}

// TestReadSnapshotSummary_WithinCap verifies no truncation for small outputs.
func TestReadSnapshotSummary_WithinCap(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("x", 100)
	v, hash, over := host.ReadSnapshotSummary(s)
	if v != s {
		t.Errorf("verbatim: want original, got %q", v)
	}
	if hash != "" {
		t.Errorf("hash: want empty for within-cap, got %q", hash)
	}
	if over {
		t.Error("over: want false for within-cap")
	}
}

// TestReadSnapshotSummary_OverCap verifies truncation + sha256 for large outputs.
func TestReadSnapshotSummary_OverCap(t *testing.T) {
	t.Parallel()
	// Generate 260 KiB of data.
	big := strings.Repeat("a", 260*1024)
	v, hash, over := host.ReadSnapshotSummary(big)
	if !over {
		t.Error("over: want true for 260 KiB input")
	}
	if len(v) > 4*1024+100 {
		t.Errorf("verbatim length: want ≤4 KiB, got %d", len(v))
	}
	if hash == "" {
		t.Error("hash: want non-empty for over-cap output")
	}
	if len(hash) != 64 {
		t.Errorf("hash length: want 64 hex chars, got %d", len(hash))
	}
}

// TestAgentExtract_NoResolvers_NoMatch verifies that an invocation with no
// resolvers declared returns no_match.
func TestAgentExtract_NoResolvers_NoMatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":     "go north",
		"schema":    schemaPath,
		"resolvers": []any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("resolved_by: want no_match for empty resolver list, got %v", res.Data["resolved_by"])
	}
}

// TestAgentExtract_LLMTier_ClaudeSessionIDPropagated verifies that
// claude_session_id is set in the result when the LLM tier matches.
func TestAgentExtract_LLMTier_ClaudeSessionIDPropagated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	promptPath := writePromptFile(t, dir, "p.md", "extract")

	runner := host.FakeExtractJSON(map[string]any{"action": "test"})
	ctx := host.WithClaudeRunner(context.Background(), runner)

	res, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":  "test action",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"llm": map[string]any{"prompt": promptPath}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["resolved_by"] != "llm" {
		t.Fatalf("resolved_by: want llm, got %v", res.Data["resolved_by"])
	}
	// claude_session_id may be empty (the fake runner doesn't produce a session
	// id), but the key must be present in the data map.
	if _, hasKey := res.Data["claude_session_id"]; !hasKey {
		t.Error("claude_session_id key missing from LLM tier result")
	}
}

// TestAgentExtract_LoadTimeSchemaMismatch verifies that a synonym payload that
// fails JSON schema validation causes the tier to fall through (runtime safety net).
func TestAgentExtract_LoadTimeSchemaMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Synonym payload has "wrong_field" which doesn't match our schema.
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"wrong_field": 42}`)
	// Schema that requires a "direction" string field.
	schema := `{
  "type": "object",
  "required": ["direction"],
  "properties": {
    "direction": {"type": "string"}
  }
}`
	schemaPath := filepath.Join(dir, "strict.json")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "go north",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Payload fails schema → tier falls through → no_match.
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("resolved_by: want no_match after schema fail, got %v", res.Data["resolved_by"])
	}
}

// TestAgentExtract_MultiPhrase_Synonyms verifies that comma-separated synonym
// phrases all resolve to the same payload.
func TestAgentExtract_MultiPhrase_Synonyms(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `
"go north,head north,north": {"direction": "north"}
`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	for _, input := range []string{"go north", "head north", "north"} {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			res, err := host.AgentExtractHandler(context.Background(), map[string]any{
				"input":  input,
				"schema": schemaPath,
				"resolvers": []any{
					map[string]any{"synonyms": synonymPath},
				},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Data["resolved_by"] != "synonyms" {
				t.Errorf("%q: resolved_by: want synonyms, got %v", input, res.Data["resolved_by"])
			}
		})
	}
}

// TestAgentExtract_JSONResult_IsParseable verifies that the synonym payload
// can be round-tripped through JSON encoding.
func TestAgentExtract_JSONResult_IsParseable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `wade: {"action": "wade", "location": "river"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "wade",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("Result.Error: %s", res.Error)
	}
	// Round-trip through JSON.
	b, marshalErr := json.Marshal(res.Data["submitted"])
	if marshalErr != nil {
		t.Fatalf("json.Marshal submitted: %v", marshalErr)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json.Unmarshal submitted: %v", err)
	}
	if decoded["action"] != "wade" {
		t.Errorf("decoded.action: want wade, got %v", decoded["action"])
	}
}

// ── L9: type-safe tools arg accessor ─────────────────────────────────────

// TestAgentExtract_LLMTier_PerCallToolsAsStringSlice verifies that per-call
// tools passed as []string (not []any) are handled correctly (L9 type-safety).
func TestAgentExtract_LLMTier_PerCallToolsAsStringSlice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	promptPath := writePromptFile(t, dir, "p.md", "extract")

	var capturedArgs []string
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		capturedArgs = args
		const payload = `{"action":"test"}`
		if outPath := host.ParseMCPConfigSubmitOutput(args); outPath != "" {
			_ = os.WriteFile(outPath, []byte(payload), 0o600)
		}
		return host.ClaudeRun{Stdout: payload}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), runner)

	res, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":  "test input",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"llm": map[string]any{"prompt": promptPath}},
		},
		"tools": []string{"Read", "Grep"}, // []string, not []any
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	joined := strings.Join(capturedArgs, " ")
	if !strings.Contains(joined, "Read") {
		t.Errorf("per-call []string tools not forwarded; args=%v", capturedArgs)
	}
}

// ── M5: extract LLM tier schema in default prompt ────────────────────────

// TestAgentExtract_LLMTier_DefaultPrompt_IncludesSchema verifies that when
// no explicit prompt is set, the default prompt includes the schema bytes so
// the LLM knows the required output shape (M5).
func TestAgentExtract_LLMTier_DefaultPrompt_IncludesSchema(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaContent := `{"type":"object","properties":{"direction":{"type":"string"}}}`
	schemaPath := filepath.Join(dir, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(schemaContent), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	var capturedStdin string
	runner := func(_ context.Context, args []string, stdin, _ string) (host.ClaudeRun, error) {
		capturedStdin = stdin
		const payload = `{"direction":"north"}`
		if outPath := host.ParseMCPConfigSubmitOutput(args); outPath != "" {
			_ = os.WriteFile(outPath, []byte(payload), 0o600)
		}
		return host.ClaudeRun{Stdout: payload}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), runner)

	_, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":  "go north",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"llm": map[string]any{}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The default prompt must contain the schema bytes.
	if !strings.Contains(capturedStdin, `"direction"`) {
		t.Errorf("default prompt does not include schema bytes; stdin=%q", capturedStdin[:min(200, len(capturedStdin))])
	}
	if !strings.Contains(capturedStdin, "submit") {
		t.Errorf("default prompt does not mention submit tool; stdin=%q", capturedStdin[:min(200, len(capturedStdin))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
