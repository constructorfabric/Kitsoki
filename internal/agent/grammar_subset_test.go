// grammar_subset_test.go is a pure unit test of GrammarSubsetOK: it accepts the
// real judge_verdict.json shape and rejects each construct llama.cpp cannot
// translate faithfully. No network, no model — fast by construction.

package agent

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// TestGrammarSubsetAcceptsJudgeVerdict verifies the canonical decide schema
// (flat object, enums, scalar number with bounds) is inside the subset, since
// it is the headline case the local-model grammar tier targets.
func TestGrammarSubsetAcceptsJudgeVerdict(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../../stories/pr-refinement/schemas/judge_verdict.json")
	if err != nil {
		t.Fatalf("read judge_verdict.json: %v", err)
	}
	if err := GrammarSubsetOK(json.RawMessage(b)); err != nil {
		t.Fatalf("GrammarSubsetOK(judge_verdict): unexpected error: %v", err)
	}
}

// TestGrammarSubsetAcceptsSimpleShapes verifies nested objects, scalar leaves,
// enums, and homogeneous arrays — the documented accepted set — all pass.
func TestGrammarSubsetAcceptsSimpleShapes(t *testing.T) {
	t.Parallel()

	accepted := map[string]string{
		"flat object":      `{"type":"object","properties":{"a":{"type":"string"},"n":{"type":"number"}}}`,
		"enum":             `{"type":"string","enum":["x","y","z"]}`,
		"scalar":           `{"type":"integer","minimum":0}`,
		"typed array":      `{"type":"array","items":{"type":"string"}}`,
		"nested object":    `{"type":"object","properties":{"inner":{"type":"object","properties":{"v":{"type":"boolean"}}}}}`,
		"anchored ptn":     `{"type":"string","pattern":"^[a-z]+$"}`,
		"standalone oneOf": `{"oneOf":[{"type":"string"},{"type":"number"}]}`,
		"addlProps false":  `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`,
		"addlProps schema": `{"type":"object","additionalProperties":{"type":"string"}}`,
		"empty":            ``,
	}
	for name, s := range accepted {
		if err := GrammarSubsetOK(json.RawMessage(s)); err != nil {
			t.Errorf("%s: expected accept, got error: %v", name, err)
		}
	}
}

// TestGrammarSubsetRejectsConstructs verifies every out-of-subset construct is
// rejected with an error naming the construct. Each case must fail; if the
// helper is removed the whole suite stops compiling, and if any case is wrongly
// accepted this test fails — that is what proves the gate has teeth.
func TestGrammarSubsetRejectsConstructs(t *testing.T) {
	t.Parallel()

	rejected := map[string]struct {
		schema string
		want   string // substring expected in the error
	}{
		"$ref":                   {`{"type":"object","properties":{"a":{"$ref":"#/$defs/x"}}}`, "$ref"},
		"$defs":                  {`{"$defs":{"x":{"type":"string"}},"type":"object"}`, "$defs"},
		"uniqueItems":            {`{"type":"array","items":{"type":"string"},"uniqueItems":true}`, "uniqueItems"},
		"not":                    {`{"not":{"type":"string"}}`, "not"},
		"if":                     {`{"if":{"type":"string"},"then":{"type":"number"}}`, "if"},
		"dependentSchemas":       {`{"type":"object","dependentSchemas":{"a":{"type":"string"}}}`, "dependentSchemas"},
		"contains":               {`{"type":"array","contains":{"type":"string"}}`, "contains"},
		"anyOf+siblings":         {`{"type":"object","properties":{"a":{"type":"string"}},"anyOf":[{"required":["a"]}]}`, "anyOf"},
		"oneOf+type":             {`{"type":"string","oneOf":[{"const":"a"},{"const":"b"}]}`, "oneOf"},
		"unanchored pattern":     {`{"type":"string","pattern":"[a-z]+"}`, "pattern"},
		"nested reject":          {`{"type":"object","properties":{"a":{"type":"array","items":{"$ref":"#/x"}}}}`, "$ref"},
		"addlProps hides ref":    {`{"type":"object","additionalProperties":{"$ref":"#/$defs/x"}}`, "$ref"},
		"patternProps hides not": {`{"type":"object","patternProperties":{"^x$":{"not":{"type":"string"}}}}`, "not"},
	}
	for name, tc := range rejected {
		err := GrammarSubsetOK(json.RawMessage(tc.schema))
		if err == nil {
			t.Errorf("%s: expected reject, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q does not mention %q", name, err.Error(), tc.want)
		}
	}
}

// TestGrammarFailClosedValidatesSubmission is the fail-closed half of the
// local-model grammar contract: even where GrammarSubsetOK accepts a schema and
// the (best-effort, fail-open) grammar tier runs, ValidateSubmission remains the
// sole guarantee. A grammar that llama.cpp ignored or applied loosely can still
// yield an off-schema Submission; that Submission MUST be rejected with a typed
// *AskError{Kind:"schema_invalid"} so the dispatcher's validation-reject
// fallback can fire. The schema here is the same one GrammarSubsetOK accepts in
// TestGrammarSubsetAcceptsJudgeVerdict, proving subset-acceptance does not imply
// submission-validity.
//
// Test rigor: ValidateSubmission is the gate under test. If it returned nil for
// the off-schema submission (regression in the schema_invalid path) the
// errors.As + Kind assertions below fail; a malformed-JSON path is covered by
// the second case. The valid submission proves the gate does not over-reject.
func TestGrammarFailClosedValidatesSubmission(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile("../../stories/pr-refinement/schemas/judge_verdict.json")
	if err != nil {
		t.Fatalf("read judge_verdict.json: %v", err)
	}

	// In-subset schema (accepted by GrammarSubsetOK) — guard the premise so this
	// test stays the fail-closed companion to TestGrammarSubsetAcceptsJudgeVerdict.
	if subErr := GrammarSubsetOK(json.RawMessage(schema)); subErr != nil {
		t.Fatalf("premise: judge_verdict must be in-subset, got: %v", subErr)
	}

	cases := map[string]struct {
		submission string
		wantKind   string
	}{
		// verdict is not one of the enum values; this is the kind of drift a
		// loosely-applied or ignored grammar leaves behind.
		"enum violation": {
			submission: `{"verdict":"maybe","intent":"accept","reason":"because","confidence":0.5}`,
			wantKind:   "schema_invalid",
		},
		// confidence out of [0,1] bounds.
		"bound violation": {
			submission: `{"verdict":"pass","intent":"accept","reason":"because","confidence":1.7}`,
			wantKind:   "schema_invalid",
		},
		// extra field with additionalProperties:false.
		"extra field": {
			submission: `{"verdict":"pass","intent":"accept","reason":"because","confidence":0.5,"x":1}`,
			wantKind:   "schema_invalid",
		},
		// not valid JSON at all.
		"malformed json": {
			submission: `{"verdict":`,
			wantKind:   "schema_invalid",
		},
	}
	for name, tc := range cases {
		verr := ValidateSubmission(json.RawMessage(schema), json.RawMessage(tc.submission))
		if verr == nil {
			t.Errorf("%s: ValidateSubmission accepted an off-schema submission (fail-closed broken)", name)
			continue
		}
		var ae *AskError
		if !errors.As(verr, &ae) {
			t.Errorf("%s: error is %T, want *AskError", name, verr)
			continue
		}
		if ae.Kind != tc.wantKind {
			t.Errorf("%s: AskError.Kind = %q, want %q", name, ae.Kind, tc.wantKind)
		}
	}

	// A submission that satisfies every constraint must pass — the gate does not
	// over-reject the very shape the grammar tier targets.
	good := `{"verdict":"pass","intent":"accept","reason":"looks good","confidence":0.9}`
	if okErr := ValidateSubmission(json.RawMessage(schema), json.RawMessage(good)); okErr != nil {
		t.Errorf("valid submission rejected: %v", okErr)
	}
}
