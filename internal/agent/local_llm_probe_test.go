// local_llm_probe_test.go is the stateless decide probe for the local-model
// backend: it drives NewLocalLLM (endpoint mode) against a fixed OpenAI
// /v1/chat/completions body shaped like the real judge_verdict decide verdict,
// then runs the returned Submission through ValidateSubmission against the
// canonical decide schema. It proves the end-to-end local-model decide contract
// (request shape → Submission → schema validity) without a live model,
// subprocess, or download — budgeted in ms.
//
// The default test is fake-HTTP-backed and deterministic. A live A/B variant
// (against a real llama-server) is gated behind KITSOKI_PROBE_LOCAL_MODEL=1 so
// the suite never spends money or spins up a model by default (memory: no LLM
// tests by default).

package agent

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

// judgeVerdictSchemaPath is the canonical decide schema the probe validates
// against. It lives in the pr-refinement story and is the headline shape the
// local-model grammar tier targets.
const judgeVerdictSchemaPath = "../../stories/pr-refinement/schemas/judge_verdict.json"

// probeVerdict is a fixed, schema-valid decide verdict the fake llama-server
// returns as the assistant message content. It must satisfy judge_verdict.json.
const probeVerdict = `{"verdict":"pass","intent":"accept","reason":"the change is correct and tested","confidence":0.88}`

// TestLocalLLMDecideProbe drives NewLocalLLM against a fixed OpenAI body and
// asserts the resulting Submission validates against the decide schema. This is
// the stateless decide probe: it ties the transport (step 1) to the validation
// authority (validate.go) for the exact judge_verdict shape.
//
// Test rigor: the fake returns probeVerdict as the chat content; the assertion
// is ValidateSubmission(schema, resp.Submission)==nil with the real
// judge_verdict.json. Without local_llm.go the package does not compile
// (NewLocalLLM undefined); if local_llm dropped the content into Submission or
// ValidateSubmission regressed, the validity assertion fails. A negative
// sub-case feeds an off-schema content through the SAME transport and asserts
// schema_invalid, proving the probe's validity check has teeth.
func TestLocalLLMDecideProbe(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile(judgeVerdictSchemaPath)
	if err != nil {
		t.Fatalf("read judge_verdict.json: %v", err)
	}

	// Premise guard: the schema must be in the translatable subset, since the
	// probe exercises the grammar-eligible decide path.
	if subErr := GrammarSubsetOK(json.RawMessage(schema)); subErr != nil {
		t.Fatalf("premise: judge_verdict must be in-subset, got: %v", subErr)
	}

	// grammar:true + in-subset schema → response_format json_schema is attached,
	// exactly as a real decide call would build it.
	o := newLocalLLMForTest(&localChatHandler{
		content: probeVerdict,
		usage:   chatUsage{PromptTokens: 120, CompletionTokens: 24},
	}, "qwen2.5-1.5b", true)
	defer o.Close()

	req := sampleRequest()
	req.Verb = "decide"
	req.SchemaJSON = json.RawMessage(schema)

	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	// The Submission must be exactly the model content (local_llm does not
	// validate — validate.go is the sole authority).
	if string(resp.Submission) != probeVerdict {
		t.Errorf("Submission: got %s, want %s", resp.Submission, probeVerdict)
	}

	// The headline assertion: the local-model decide Submission validates.
	if vErr := ValidateSubmission(json.RawMessage(schema), resp.Submission); vErr != nil {
		t.Errorf("decide probe Submission failed schema validation: %v", vErr)
	}

	// Grammar should be reported applied (in-subset schema, grammar enabled).
	if resp.Meta["grammar"] != true {
		t.Errorf("Meta.grammar: got %v, want true", resp.Meta["grammar"])
	}

	// Negative sub-case: an off-schema content through the SAME transport must be
	// caught by ValidateSubmission — proving the validity assertion above is not
	// vacuous.
	bad := newLocalLLMForTest(&localChatHandler{
		content: `{"verdict":"definitely","intent":"accept","reason":"x","confidence":2.0}`,
	}, "qwen2.5-1.5b", true)
	defer bad.Close()
	badResp, err := bad.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask (bad): %v", err)
	}
	if vErr := ValidateSubmission(json.RawMessage(schema), badResp.Submission); vErr == nil {
		t.Error("off-schema decide Submission unexpectedly validated (probe check is vacuous)")
	}
}

// TestLocalLLMSlugProbe_CodeFence verifies that a model response wrapped in
// markdown code fences is transparently stripped and validates against slug.json.
// This is the regression test for the bug where qwen2.5-1.5b-instruct returned
// ```json\n{...}\n``` despite grammar constraints, causing schema_invalid.
func TestLocalLLMSlugProbe_CodeFence(t *testing.T) {
	t.Parallel()

	schema, err := os.ReadFile("../../stories/dev-story/schemas/slug.json")
	if err != nil {
		t.Fatalf("read slug.json: %v", err)
	}

	if subErr := GrammarSubsetOK(json.RawMessage(schema)); subErr != nil {
		t.Fatalf("premise: slug.json must be in-subset: %v", subErr)
	}

	// Simulate a model that wraps its JSON in a code fence (the bug).
	fencedContent := "```json\n{\"slug\":\"virtual-pets\",\"rationale\":\"interactive pet companion\"}\n```"

	o := newLocalLLMForTest(&localChatHandler{
		content: fencedContent,
		usage:   chatUsage{PromptTokens: 80, CompletionTokens: 18},
	}, "qwen2.5-1.5b", true)
	defer o.Close()

	req := sampleRequest()
	req.Verb = "decide"
	req.SchemaJSON = json.RawMessage(schema)

	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if vErr := ValidateSubmission(json.RawMessage(schema), resp.Submission); vErr != nil {
		t.Errorf("fenced slug Submission failed schema validation: %v\nsubmission: %s", vErr, resp.Submission)
	}
	// Stripped content must be valid JSON, not contain fences.
	if string(resp.Submission[0]) != "{" {
		t.Errorf("Submission does not start with '{' after strip: %s", resp.Submission)
	}
}

// TestLocalLLMDecideProbe_Live is the opt-in A/B variant against a real
// llama-server. It is skipped unless KITSOKI_PROBE_LOCAL_MODEL=1, honouring the
// no-LLM-tests-by-default rule. When enabled it points NewLocalLLM at
// KITSOKI_LOCAL_MODEL_ENDPOINT (default http://127.0.0.1:8080) and asserts the
// real model's decide Submission validates against judge_verdict.json.
func TestLocalLLMDecideProbe_Live(t *testing.T) {
	if os.Getenv("KITSOKI_PROBE_LOCAL_MODEL") != "1" {
		t.Skip("set KITSOKI_PROBE_LOCAL_MODEL=1 to probe a live local model")
	}

	schema, err := os.ReadFile(judgeVerdictSchemaPath)
	if err != nil {
		t.Fatalf("read judge_verdict.json: %v", err)
	}

	endpoint := os.Getenv("KITSOKI_LOCAL_MODEL_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8080"
	}
	model := os.Getenv("KITSOKI_LOCAL_MODEL")
	if model == "" {
		model = "qwen2.5-1.5b"
	}

	o := NewLocalLLM(model, 0, "", true, endpoint, nil)
	defer o.Close()

	req := sampleRequest()
	req.Verb = "decide"
	req.PromptText = "The PR fixes the reported bug and adds a regression test. Return your verdict."
	req.SchemaJSON = json.RawMessage(schema)

	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("live Ask: %v", err)
	}
	if vErr := ValidateSubmission(json.RawMessage(schema), resp.Submission); vErr != nil {
		t.Errorf("live decide Submission failed schema validation: %v\nsubmission: %s", vErr, resp.Submission)
	}
	t.Logf("live decide Submission: %s (meta: %v)", resp.Submission, resp.Meta)
}

// TestLocalLLMSlugProbe_Live is the opt-in slug-naming integration test against
// a real local model. Uses the same KITSOKI_PROBE_LOCAL_MODEL=1 gate as the
// decide probe. Validates that slug.json grammar is applied and the model
// produces a valid slug — specifically exercising the code-fence strip path that
// was found to fail in dogfood (qwen2.5-1.5b wraps its output in ```json fences
// despite grammar constraints).
func TestLocalLLMSlugProbe_Live(t *testing.T) {
	if os.Getenv("KITSOKI_PROBE_LOCAL_MODEL") != "1" {
		t.Skip("set KITSOKI_PROBE_LOCAL_MODEL=1 to probe a live local model")
	}

	schema, err := os.ReadFile("../../stories/dev-story/schemas/slug.json")
	if err != nil {
		t.Fatalf("read slug.json: %v", err)
	}

	endpoint := os.Getenv("KITSOKI_LOCAL_MODEL_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8080"
	}
	model := os.Getenv("KITSOKI_LOCAL_MODEL")
	if model == "" {
		model = "qwen2.5-1.5b"
	}

	o := NewLocalLLM(model, 0, "", true, endpoint, nil)
	defer o.Close()

	req := sampleRequest()
	req.Verb = "decide"
	req.PromptText = `Turn the idea below into a short kebab-case slug (2-5 words, lowercase).

The idea:

> local model agent for routing decisions

Return ONLY a raw JSON object — no prose, no markdown, no code fences:
{"slug": "my-proposal-slug", "rationale": "one line why"}

Do NOT wrap the JSON in ` + "```" + `json … ` + "```" + ` or any other formatting.`
	req.SchemaJSON = json.RawMessage(schema)

	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("live slug Ask: %v", err)
	}
	if vErr := ValidateSubmission(json.RawMessage(schema), resp.Submission); vErr != nil {
		t.Errorf("live slug Submission failed schema validation: %v\nsubmission: %s", vErr, resp.Submission)
	}
	t.Logf("live slug Submission: %s (meta: %v)", resp.Submission, resp.Meta)
}
