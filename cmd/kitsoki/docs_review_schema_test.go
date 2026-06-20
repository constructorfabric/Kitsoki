// docs_review_schema_test.go — Pins the docs-review story's verdict
// schema against the exact set of malformations that have produced
// silent "happy state, missing fields" rendering bugs in the past.
//
// The schema lives at stories/docs-review/schemas/docs_review_verdict.json
// and is consumed by host.agent.decide at runtime. The validator is
// authoritative — if the schema stops rejecting these shapes, the story
// can land in `reviewed` with `(missing)` everywhere and we won't notice
// until an operator complains.
//
// Each table case names a previously-observed failure mode in the
// comment so a future maintainer who tries to relax a constraint sees
// why it exists.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// docsReviewSchemaPath resolves the on-disk schema relative to this
// test file. Avoids hard-coded absolute paths so the tests run under
// `go test ./...` from any working directory.
func docsReviewSchemaPath(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	return filepath.Join(repoRoot, "stories", "docs-review", "schemas", "docs_review_verdict.json")
}

// loadDocsReviewSchema reads the schema bytes once per test invocation.
// We deliberately do NOT cache across tests so a schema edit between
// runs picks up immediately.
func loadDocsReviewSchema(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(docsReviewSchemaPath(t))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	return raw
}

// fullValidVerdict is the canonical shape the docs-review agent is
// supposed to submit. Helper for the "small mutation" rejection tests.
const fullValidVerdict = `{
  "decision": "needs_update",
  "summary": "docs/foo.md is stale relative to commit abc1234",
  "confidence": 0.7,
  "commits": [
    {"sha": "abc1234", "subject": "feat: bar", "change": "adds a new public flag"}
  ],
  "stale_docs": [
    {
      "path": "docs/foo.md",
      "lines": "10-20",
      "anchor": "§2 Configuration",
      "invalidations": [
        {
          "commit": "abc1234",
          "source": "cmd/foo/main.go",
          "change": "new --bar flag",
          "reason": "the flag table does not list --bar"
        }
      ]
    }
  ],
  "recommended_actions": ["document --bar in §2"]
}`

// TestDocsReviewSchema_AcceptsCanonicalVerdict pins the happy path so a
// stray edit that breaks a real submission gets caught.
func TestDocsReviewSchema_AcceptsCanonicalVerdict(t *testing.T) {
	schema := loadDocsReviewSchema(t)
	var stdout, stderr bytes.Buffer
	if err := runValidateOnce(strings.NewReader(fullValidVerdict), &stdout, &stderr, schema, "docs_review_verdict"); err != nil {
		t.Fatalf("canonical payload rejected: %v\nstderr: %s", err, stderr.String())
	}
}

// TestDocsReviewSchema_AcceptsUpToDate covers the "no findings" branch
// — decision=up_to_date with empty stale_docs[] is a valid verdict.
func TestDocsReviewSchema_AcceptsUpToDate(t *testing.T) {
	schema := loadDocsReviewSchema(t)
	payload := `{
		"decision": "up_to_date",
		"summary": "Only go.sum updates; no doc-visible changes.",
		"confidence": 0.9,
		"commits": [{"sha": "aaa1111", "subject": "chore: deps", "change": "go.sum bump only"}],
		"stale_docs": []
	}`
	var stdout, stderr bytes.Buffer
	if err := runValidateOnce(strings.NewReader(payload), &stdout, &stderr, schema, "docs_review_verdict"); err != nil {
		t.Fatalf("up_to_date payload rejected: %v\nstderr: %s", err, stderr.String())
	}
}

// TestDocsReviewSchema_RejectsEmpty pins the bug we just hit live: the
// agent submits `{}` (or agent.decide returns ok with no submit) and
// nothing flags it. The schema MUST reject this so host.agent.decide
// loops back for a retry instead of binding empty into world.verdict.
func TestDocsReviewSchema_RejectsEmpty(t *testing.T) {
	schema := loadDocsReviewSchema(t)
	var stdout, stderr bytes.Buffer
	if err := runValidateOnce(strings.NewReader(`{}`), &stdout, &stderr, schema, "docs_review_verdict"); err == nil {
		t.Fatalf("expected empty {} to be rejected; got nil err\nstdout: %s", stdout.String())
	}
}

// TestDocsReviewSchema_RejectsMutations table-tests every single-field
// mutation we've seen the LLM produce. If any of these slips past the
// validator the story renders garbage in `reviewed` instead of bouncing
// to `review_failed`.
func TestDocsReviewSchema_RejectsMutations(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		// reasonForRejection documents which schema constraint the
		// payload violates. Diagnostic only; the test asserts only
		// that runValidateOnce returns an error.
		reasonForRejection string
	}{
		{
			name: "missing decision",
			payload: `{
				"summary": "x", "confidence": 0.5, "commits": [], "stale_docs": []
			}`,
			reasonForRejection: "required: decision",
		},
		{
			name: "decision out of enum",
			payload: `{
				"decision": "maybe", "summary": "x", "confidence": 0.5,
				"commits": [], "stale_docs": []
			}`,
			reasonForRejection: "enum: needs_update|up_to_date",
		},
		{
			name: "confidence above 1",
			payload: `{
				"decision": "up_to_date", "summary": "x", "confidence": 1.5,
				"commits": [], "stale_docs": []
			}`,
			reasonForRejection: "maximum: 1",
		},
		{
			name: "summary too short",
			payload: `{
				"decision": "up_to_date", "summary": "x", "confidence": 0.5,
				"commits": [], "stale_docs": []
			}`,
			reasonForRejection: "minLength: 10",
		},
		{
			name: "missing commits",
			payload: `{
				"decision": "up_to_date", "summary": "long enough summary",
				"confidence": 0.5, "stale_docs": []
			}`,
			reasonForRejection: "required: commits",
		},
		{
			name: "missing stale_docs",
			payload: `{
				"decision": "up_to_date", "summary": "long enough summary",
				"confidence": 0.5, "commits": []
			}`,
			reasonForRejection: "required: stale_docs",
		},
		{
			name: "stale_doc missing lines (recurring LLM bug)",
			payload: `{
				"decision": "needs_update", "summary": "long enough summary",
				"confidence": 0.5,
				"commits": [{"sha":"a","subject":"s","change":"c"}],
				"stale_docs": [
					{"path": "docs/foo.md", "invalidations": [{"commit":"a","change":"c","reason":"r"}]}
				]
			}`,
			reasonForRejection: "stale_docs[].lines required",
		},
		{
			name: "stale_doc empty invalidations",
			payload: `{
				"decision": "needs_update", "summary": "long enough summary",
				"confidence": 0.5,
				"commits": [{"sha":"a","subject":"s","change":"c"}],
				"stale_docs": [
					{"path": "docs/foo.md", "lines": "1-2", "invalidations": []}
				]
			}`,
			reasonForRejection: "invalidations minItems: 1",
		},
		{
			name: "stale_doc with extra field",
			payload: `{
				"decision": "needs_update", "summary": "long enough summary",
				"confidence": 0.5,
				"commits": [{"sha":"a","subject":"s","change":"c"}],
				"stale_docs": [
					{"path": "docs/foo.md", "lines": "1-2", "rogue": "no",
					 "invalidations": [{"commit":"a","change":"c","reason":"r"}]}
				]
			}`,
			reasonForRejection: "additionalProperties: false at stale_docs[]",
		},
		{
			name: "commit missing change",
			payload: `{
				"decision": "needs_update", "summary": "long enough summary",
				"confidence": 0.5,
				"commits": [{"sha":"a","subject":"s"}],
				"stale_docs": []
			}`,
			reasonForRejection: "commits[].change required",
		},
		{
			name: "invalidation missing reason",
			payload: `{
				"decision": "needs_update", "summary": "long enough summary",
				"confidence": 0.5,
				"commits": [{"sha":"a","subject":"s","change":"c"}],
				"stale_docs": [
					{"path": "docs/foo.md", "lines": "1-2",
					 "invalidations": [{"commit":"a","change":"c"}]}
				]
			}`,
			reasonForRejection: "invalidations[].reason required",
		},
		{
			name: "top-level rogue field",
			payload: `{
				"decision": "up_to_date", "summary": "long enough summary",
				"confidence": 0.5, "commits": [], "stale_docs": [],
				"rationale_md": "should be rejected"
			}`,
			reasonForRejection: "additionalProperties: false at root",
		},
	}

	schema := loadDocsReviewSchema(t)
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runValidateOnce(strings.NewReader(tc.payload), &stdout, &stderr, schema, "docs_review_verdict")
			if err == nil {
				t.Fatalf("payload accepted but should have failed (%s)\npayload: %s\nstdout: %s",
					tc.reasonForRejection, tc.payload, stdout.String())
			}
		})
	}
}
