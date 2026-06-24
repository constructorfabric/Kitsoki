// local_llm_cassette_test.go — local-model transport coverage for the
// conformance gauntlet (step 5a).
//
// The cassette transport is the deterministic stand-in for a recorded
// builtin.local_llm run: a hand-seeded EpisodeAgent fixture
// (testdata/local_llm_decide.cassette.yaml) carries a decide verdict and an
// extract turn as if they came from the alias "agent.local". Replaying it
// through NewCassetteAgent must reproduce the seeded Submission bytes
// identically — that is the cross-transport invariant the four-transport
// gauntlet asserts (TestConformance_FourTransports), here specialised to the
// local-model-shaped Submission. Meta (episode_id / match_idx / call_id) is
// transport-specific and deliberately NOT compared.
package testrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/agent"
)

// localCassettePath is the committed seeded fixture.
const localCassettePath = "testdata/local_llm_decide.cassette.yaml"

// TestConformance_LocalLLMCassette loads the seeded local-model cassette and
// asserts that replaying the decide and extract episodes through the cassette
// agent yields the exact seeded Submission bytes, and that the decide
// Submission validates against the canonical judge_verdict schema.
//
// Test rigor: the assertions compare resp.Submission to the byte-for-byte
// response string embedded in the fixture. If the fixture is absent LoadCassette
// errors (so the test fails without the committed file). If the cassette
// transport dropped or reshaped the local-model Submission, the byte-equality
// and ValidateSubmission checks fail. The schema-validity assertion ties the
// replayed local-model Submission to the same validation authority the live
// transport relies on.
func TestConformance_LocalLLMCassette(t *testing.T) {
	t.Parallel()

	abs, err := filepath.Abs(localCassettePath)
	if err != nil {
		t.Fatalf("abs cassette path: %v", err)
	}
	cas, err := LoadCassette(abs)
	if err != nil {
		t.Fatalf("LoadCassette(%s): %v", localCassettePath, err)
	}

	// Register the cassette under the same alias the fixture's episodes match on.
	o := NewCassetteAgent(cas, "agent.local", func() string { return "" }, nil)
	defer o.Close()

	// ── decide verdict: must replay byte-identically and validate ─────────────
	const wantDecide = `{"verdict":"pass","intent":"accept","reason":"the change is correct and tested","confidence":0.88}`
	decideResp, err := o.Ask(context.Background(), agent.AskRequest{Verb: "decide"})
	if err != nil {
		t.Fatalf("decide Ask: %v", err)
	}
	if string(decideResp.Submission) != wantDecide {
		t.Errorf("decide Submission mismatch:\n  got:  %s\n  want: %s", decideResp.Submission, wantDecide)
	}
	// Transport tag is cassette-specific; Submission (above) is the cross-transport invariant.
	if decideResp.Meta["transport"] != "cassette" {
		t.Errorf("decide Meta.transport: got %v, want cassette", decideResp.Meta["transport"])
	}

	// The replayed local-model decide Submission must satisfy the decide schema —
	// the same validation authority the live builtin.local_llm transport relies on.
	schema, err := os.ReadFile("../../stories/pr-refinement/schemas/judge_verdict.json")
	if err != nil {
		t.Fatalf("read judge_verdict.json: %v", err)
	}
	if vErr := agent.ValidateSubmission(json.RawMessage(schema), decideResp.Submission); vErr != nil {
		t.Errorf("replayed decide Submission failed schema validation: %v", vErr)
	}

	// ── extract turn: must replay byte-identically ────────────────────────────
	const wantExtract = `{"ticket":"CLOUD-42","priority":"high"}`
	extractResp, err := o.Ask(context.Background(), agent.AskRequest{Verb: "extract"})
	if err != nil {
		t.Fatalf("extract Ask: %v", err)
	}
	if string(extractResp.Submission) != wantExtract {
		t.Errorf("extract Submission mismatch:\n  got:  %s\n  want: %s", extractResp.Submission, wantExtract)
	}

	// No phantom episodes: both seeded episodes were exercised.
	if un := cas.UnmatchedEpisodes(); len(un) != 0 {
		t.Errorf("unmatched episodes after replay: %v", un)
	}
}
