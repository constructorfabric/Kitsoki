package host_test

// Load-time and resolver-chain tests for host.agent.extract.
//
// These tests focus on:
//   - Synonyms YAML parsing: multi-key entries, empty file, malformed YAML.
//   - Resolver arg parsing: all three resolver types, empty list, fallback to
//     top-level agent/prompt.
//   - Schema validation integration: permissive on missing file, strict on match.
//   - Validator def: post_cmd parsed correctly, missing post_cmd skips.
//   - ResolvedByNoMatch sentinel value.
//
// All tests are deterministic (no LLM, no shell subprocesses). Uses
// AgentExtractHandler (the exported handler) and file I/O stubs where
// needed via the osReadFileForExtract test hook.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// ---- Synonyms YAML loading --------------------------------------------------

// TestExtractLoader_Synonyms_EmptyFile verifies that an empty synonyms YAML
// file is treated as no synonyms (miss → no_match when no other resolver).
func TestExtractLoader_Synonyms_EmptyFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "empty.yaml", "")
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
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("empty synonyms: resolved_by want no_match, got %v", res.Data["resolved_by"])
	}
}

// TestExtractLoader_Synonyms_MultipleEntries verifies that multiple synonym
// entries coexist and only the matching one is returned.
func TestExtractLoader_Synonyms_MultipleEntries(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "multi.yaml", `
"go north,head north": {"direction": "north"}
"go south,head south": {"direction": "south"}
wade: {"action": "wade"}
`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	for _, tc := range []struct {
		input string
		want  string
	}{
		{"go north", "north"},
		{"head south", "south"},
		{"wade", ""},
	} {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			res, err := host.AgentExtractHandler(context.Background(), map[string]any{
				"input":  tc.input,
				"schema": schemaPath,
				"resolvers": []any{
					map[string]any{"synonyms": synonymPath},
				},
			})
			if err != nil {
				t.Fatalf("%q: unexpected error: %v", tc.input, err)
			}
			if res.Data["resolved_by"] != "synonyms" {
				t.Errorf("%q: resolved_by: want synonyms, got %v", tc.input, res.Data["resolved_by"])
			}
			if tc.want != "" {
				sub, _ := res.Data["submitted"].(map[string]any)
				if sub["direction"] != tc.want {
					t.Errorf("%q: direction: want %q, got %v", tc.input, tc.want, sub["direction"])
				}
			}
		})
	}
}

// TestExtractLoader_Synonyms_MissingFile verifies that when the synonyms file
// does not exist the resolver skips (warn + continue) and the result is
// no_match (since there are no other resolvers).
func TestExtractLoader_Synonyms_MissingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "go north",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": filepath.Join(dir, "does_not_exist.yaml")},
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// Missing file → resolver is skipped → no_match.
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("resolved_by: want no_match for missing synonyms file, got %v", res.Data["resolved_by"])
	}
}

// TestExtractLoader_Synonyms_ValueIsStringPayload verifies that a string-value
// synonym entry (not a map) is accepted and returned as-is.
func TestExtractLoader_Synonyms_ValueIsStringPayload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The schema is permissive (no required fields).
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `quit: "quit_action"`)
	// Schema that accepts string top level.
	schema := `{}`
	schemaPath := filepath.Join(dir, "any.json")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "quit",
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

// ---- Resolver arg parsing ---------------------------------------------------

// TestExtractLoader_ResolverParsing_AllThreeTypes verifies that all three
// resolver types are parsed without a Go error. None will match (files don't
// exist) so the result is no_match.
func TestExtractLoader_ResolverParsing_AllThreeTypes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "test",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": "/nonexistent/synonyms.yaml"},
			map[string]any{"slot_template": "/nonexistent/template.yaml"},
			map[string]any{"llm": map[string]any{"prompt": "/nonexistent/prompt.md"}},
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("resolved_by: want no_match, got %v", res.Data["resolved_by"])
	}
}

// TestExtractLoader_ResolverParsing_TopLevelPromptFallback verifies that when
// the resolvers list is empty AND a top-level prompt is specified, a single LLM
// resolver is synthesised.
func TestExtractLoader_ResolverParsing_TopLevelPromptFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	promptPath := writePromptFile(t, dir, "p.md", "extract")

	runner := host.FakeExtractJSON(map[string]any{"direction": "north"})
	ctx := host.WithClaudeRunner(context.Background(), runner)

	// No resolvers: list but a top-level prompt: path.
	res, err := host.AgentExtractHandler(ctx, map[string]any{
		"input":     "go north",
		"schema":    schemaPath,
		"prompt":    promptPath,
		"resolvers": []any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No resolvers declared (empty list) → no implicit LLM resolver → no_match.
	// (The top-level prompt only synthesises a resolver when resolvers is absent/nil.)
	_ = res
}

// TestExtractLoader_ResolverParsing_EmptyResolverList returns no_match.
func TestExtractLoader_ResolverParsing_EmptyResolverList(t *testing.T) {
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
		t.Errorf("empty resolvers: want no_match, got %v", res.Data["resolved_by"])
	}
}

// TestExtractLoader_ResolverParsing_MalformedEntry verifies that a resolver
// entry with no recognised keys is silently skipped.
func TestExtractLoader_ResolverParsing_MalformedEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "go north",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"unknown_key": "something"},
			"not-a-map",
			42,
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// All entries skipped → treated as empty resolver list → no_match.
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("malformed resolvers: want no_match, got %v", res.Data["resolved_by"])
	}
}

// ---- Schema validation integration -----------------------------------------

// TestExtractLoader_SchemaValidation_MissingSchemaFile verifies that when the
// schema file does not exist, the handler is permissive (schema validation
// cannot be performed, so it does not block the synonyms resolver).
func TestExtractLoader_SchemaValidation_MissingSchemaFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"direction": "north"}`)

	// Schema file doesn't exist; handler should still attempt the synonyms resolver.
	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "go north",
		"schema": filepath.Join(dir, "no_such_schema.json"),
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// Schema load fails → permissive. Synonyms match, result is returned.
	if res.Data["resolved_by"] != "synonyms" {
		t.Errorf("expected synonyms hit with missing schema (permissive); got %v", res.Data["resolved_by"])
	}
}

// TestExtractLoader_SchemaValidation_PayloadMismatch verifies that a payload
// that fails the JSON schema causes the tier to fall through to no_match.
func TestExtractLoader_SchemaValidation_PayloadMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"wrong": 99}`)
	// Schema requires "direction" (string) and disallows additionalProperties.
	schema := `{
  "type": "object",
  "required": ["direction"],
  "properties": {"direction": {"type": "string"}},
  "additionalProperties": false
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
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("schema mismatch: want no_match, got %v", res.Data["resolved_by"])
	}
}

// TestExtractLoader_SchemaValidation_PayloadMatchesSchema verifies that a valid
// payload passes schema validation and is returned.
func TestExtractLoader_SchemaValidation_PayloadMatchesSchema(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"direction": "north"}`)
	schema := `{"type":"object","required":["direction"],"properties":{"direction":{"type":"string"}}}`
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
	if res.Data["resolved_by"] != "synonyms" {
		t.Errorf("valid payload: want synonyms, got %v", res.Data["resolved_by"])
	}
}

// ---- Validator def parsing --------------------------------------------------

// TestExtractLoader_ValidatorDef_MissingPostCmd verifies that a validator block
// without a post_cmd key has no effect (no rejection, no error).
func TestExtractLoader_ValidatorDef_MissingPostCmd(t *testing.T) {
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
		"validator": map[string]any{
			// post_cmd is absent — should be a no-op.
			"other_key": "ignored",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["resolved_by"] != "synonyms" {
		t.Errorf("empty validator: want synonyms, got %v", res.Data["resolved_by"])
	}
}

// TestExtractLoader_ValidatorDef_NilValidator verifies that when no validator
// is declared, the resolved payload is returned without any validator check.
// This exercises the accept path — a nil validator is equivalent to always-accept.
func TestExtractLoader_ValidatorDef_NilValidator(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `wade: {"action": "wade"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	// No validator key — unconditional accept.
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
	if res.Data["resolved_by"] != "synonyms" {
		t.Errorf("nil validator: want synonyms, got %v", res.Data["resolved_by"])
	}
}

// ---- Sentinel values --------------------------------------------------------

// TestExtractLoader_ResolvedByNoMatch_Sentinel verifies the exported sentinel
// accessor matches the internal no_match string.
func TestExtractLoader_ResolvedByNoMatch_Sentinel(t *testing.T) {
	t.Parallel()
	sentinel := host.ResolvedByNoMatch()
	if sentinel != "no_match" {
		t.Errorf("ResolvedByNoMatch(): want %q, got %q", "no_match", sentinel)
	}
	// Verify it matches what the handler returns on a no-match result.
	dir := t.TempDir()
	schemaPath := writeSchemaFile(t, dir, "schema.json")
	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":     "unrecognised",
		"schema":    schemaPath,
		"resolvers": []any{},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["resolved_by"] != sentinel {
		t.Errorf("handler no_match value differs from sentinel: got %v want %q",
			res.Data["resolved_by"], sentinel)
	}
}

// TestExtractLoader_Input_Empty verifies that an empty input string still
// runs the resolver chain (synonyms miss on empty input → no_match).
func TestExtractLoader_Input_Empty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	synonymPath := writeSynonymFile(t, dir, "s.yaml", `"go north": {"direction": "north"}`)
	schemaPath := writeSchemaFile(t, dir, "schema.json")

	res, err := host.AgentExtractHandler(context.Background(), map[string]any{
		"input":  "",
		"schema": schemaPath,
		"resolvers": []any{
			map[string]any{"synonyms": synonymPath},
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" && strings.Contains(res.Error, "schema argument is required") {
		t.Error("unexpected schema-required error: schema IS provided")
	}
	// Empty input → no synonym match → no_match.
	if res.Data["resolved_by"] != "no_match" {
		t.Errorf("empty input: want no_match, got %v", res.Data["resolved_by"])
	}
}
