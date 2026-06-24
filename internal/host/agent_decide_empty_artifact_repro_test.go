package host_test

// Reproduction for bug 2026-06-03T121407Z-agent-decide-silent-abandon-empty-artifact
//
//   "agent.decide with no submit routes to success with an empty artifact
//    instead of failing visibly"
//
// The host.agent.decide handler grew a safety net (buildDecideResult) that
// converts a clean exit with NO submit() call into a Result.Error, so a true
// "silent abandon" now routes to on_error:. BUT the code-block recovery path in
// runDecideWithValidatorRetryLoop (agent_decide.go) opens a hole:
//
//   - When the model never calls submit() but writes a fenced ```json ... ```
//     block in its prose, the loop extracts that JSON and writes it straight to
//     the validator output file via os.WriteFile — bypassing the mcp-validator
//     entirely.
//   - Because schema validation only runs *inside* the validator MCP server, an
//     EMPTY `{}` (or any object missing required schema fields) is accepted as a
//     "recovered verdict".
//   - buildDecideResult then sees a non-empty output file, sets submittedCaptured
//     = true, reports NO error, and the success arc fires carrying an empty /
//     schema-invalid artifact.
//
// This is exactly the contract violation the ticket describes: an interpretive
// decision silently succeeds with no real payload. These tests use the existing
// no-LLM ClaudeRunner seam — no real subprocess, no cost.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/host"
)

// requireVerdictSchema writes a schema whose ONLY valid payloads contain a
// non-empty "verdict" string. An empty {} must NOT satisfy it.
func requireVerdictSchema(t *testing.T) string {
	t.Helper()
	schema := `{
  "type": "object",
  "properties": { "verdict": { "type": "string", "minLength": 1 } },
  "required": ["verdict"]
}`
	dir := t.TempDir()
	p := filepath.Join(dir, "verdict.schema.json")
	if err := os.WriteFile(p, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	return p
}

// fakeNoSubmitCodeBlockRunner simulates a model that NEVER calls the submit
// tool (it does not write the validator --output file) but emits a fenced
// ```json``` block in its prose. `body` is the JSON placed inside the fence.
func fakeNoSubmitCodeBlockRunner(body string) host.ClaudeRunner {
	return func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		// Deliberately do NOT write the validator output path: this models the
		// "submit tool was never called" condition.
		stdout := "Here is my verdict:\n\n```json\n" + body + "\n```\n"
		return host.ClaudeRun{Stdout: stdout, ExitCode: 0}, nil
	}
}

// TestAgentDecide_EmptyCodeBlockSilentlySucceeds is the headline reproduction:
// an EMPTY {} fenced block — which fails the required-"verdict" schema — is
// accepted as a recovered verdict. The decide call returns NO error and a
// `submitted` payload that is an empty map. The success arc fires with an
// empty artifact instead of routing to a *_failed state.
func TestAgentDecide_EmptyCodeBlockSilentlySucceeds(t *testing.T) {
	t.Parallel()
	schemaPath := requireVerdictSchema(t)
	ctx := host.WithClaudeRunner(context.Background(), fakeNoSubmitCodeBlockRunner("{}"))

	res, err := host.AgentDecideHandler(ctx, map[string]any{
		"prompt": "Decide the verdict.",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}

	// === BUG WITNESS ===
	// Correct behaviour: an empty / schema-invalid verdict must surface as
	// Result.Error so on_error: / a *_failed arc can route. Instead the handler
	// reports success.
	if res.Error != "" {
		t.Skipf("bug appears FIXED: empty code-block verdict now errors: %q", res.Error)
	}

	submitted, ok := res.Data["submitted"]
	if !ok {
		t.Fatalf("expected the empty {} to be (wrongly) captured as submitted; got Data=%v", res.Data)
	}
	m, isMap := submitted.(map[string]any)
	if !isMap || len(m) != 0 {
		t.Fatalf("expected an EMPTY recovered artifact (the bug); got submitted=%#v", submitted)
	}

	// We reach here only when the bug reproduces: success arc + empty artifact,
	// schema (required: verdict) never enforced.
	t.Logf("REPRODUCED: host.agent.decide returned success (Error=%q) with an empty artifact %#v; the required-\"verdict\" schema was never enforced on the recovered code block.", res.Error, submitted)
}

// TestAgentDecide_SchemaInvalidCodeBlockSilentlySucceeds widens the witness:
// a non-empty but schema-VIOLATING object (wrong field) is likewise accepted,
// proving the recovery path skips schema validation entirely rather than the
// problem being specific to the empty object.
func TestAgentDecide_SchemaInvalidCodeBlockSilentlySucceeds(t *testing.T) {
	t.Parallel()
	schemaPath := requireVerdictSchema(t)
	// "verdict" is required; this payload has only an unrelated field.
	ctx := host.WithClaudeRunner(context.Background(), fakeNoSubmitCodeBlockRunner(`{"not_the_verdict": 123}`))

	res, err := host.AgentDecideHandler(ctx, map[string]any{
		"prompt": "Decide the verdict.",
		"schema": schemaPath,
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Skipf("bug appears FIXED: schema-invalid code-block verdict now errors: %q", res.Error)
	}
	submitted, ok := res.Data["submitted"]
	if !ok {
		t.Fatalf("expected schema-invalid object to be (wrongly) captured as submitted; got Data=%v", res.Data)
	}
	m, _ := submitted.(map[string]any)
	if _, hasVerdict := m["verdict"]; hasVerdict {
		t.Fatalf("test setup wrong: payload should NOT contain required 'verdict'; got %#v", submitted)
	}
	t.Logf("REPRODUCED: host.agent.decide accepted a schema-invalid recovered verdict %#v with no error; required field 'verdict' missing.", submitted)
}
