package tui

import (
	"strings"
	"testing"
)

// TestRoutingPipeline_ProgressAndResolve walks the pipeline through a local-model
// route: deterministic misses, semantic misses, the LLM layer wins. It asserts
// the live progress advances the active marker and the resolved line names the
// winning layer, the backend, the intent, and the confidence.
func TestRoutingPipeline_ProgressAndResolve(t *testing.T) {
	t.Parallel()
	p := newRoutingPipeline()

	// Initially deterministic is active.
	if got := p.renderProgress(); !strings.Contains(got, glyphActive+" deterministic") {
		t.Fatalf("initial progress should mark deterministic active; got %q", got)
	}

	p.markMiss(TierDeterministic, "")
	if got := p.renderProgress(); !strings.Contains(got, glyphMissed+" deterministic") || !strings.Contains(got, glyphActive+" semantic") {
		t.Fatalf("after deterministic miss, semantic should be active; got %q", got)
	}

	p.markMiss(TierSemantic, "")
	if got := p.renderProgress(); !strings.Contains(got, glyphActive+" local-LLM") {
		t.Fatalf("after semantic miss, local-LLM should be active; got %q", got)
	}

	// A hit whose backend is agent.local lands on the local-LLM layer, NOT the
	// main-LLM (cloud) layer.
	p.markHit(TierLLM, "read_email", "agent.local", 0.91, true)
	if !p.resolved() {
		t.Fatal("pipeline should be resolved after a hit")
	}
	got := p.renderResolved()
	for _, want := range []string{"routed via local-LLM", "agent.local", "read_email", "0.91", "main-LLM " + glyphPending} {
		if !strings.Contains(got, want) {
			t.Errorf("resolved line missing %q; got %q", want, got)
		}
	}
}

// TestRoutingPipeline_LocalMissThenCloud covers the local-LLM miss event: the
// local tier is tried and misses (marked ✗ live), then the cloud main-LLM wins.
func TestRoutingPipeline_LocalMissThenCloud(t *testing.T) {
	t.Parallel()
	p := newRoutingPipeline()
	p.markMiss(TierDeterministic, "")
	p.markMiss(TierSemantic, "")
	// Local-LLM tried and missed (backend names agent.local) → marks that layer
	// missed and advances to main-LLM, BEFORE the cloud hit lands.
	p.markMiss(TierLLM, "agent.local")
	if got := p.renderProgress(); !strings.Contains(got, glyphMissed+" local-LLM") || !strings.Contains(got, glyphActive+" main-LLM") {
		t.Fatalf("local-LLM should be missed and main-LLM active; got %q", got)
	}
	// Cloud wins.
	p.markHit(TierLLM, "read_email", "claude-haiku", 0, false)
	got := p.renderResolved()
	if !strings.Contains(got, "routed via main-LLM") || !strings.Contains(got, "local-LLM "+glyphMissed) {
		t.Errorf("resolved should show main-LLM winner and local-LLM missed; got %q", got)
	}
}

// TestRoutingPipeline_ResolveFromProvenance covers the completion-time fallback:
// an "llm" provenance names the backend; an empty provenance (main-turn) is
// labelled claude.
func TestRoutingPipeline_ResolveFromProvenance(t *testing.T) {
	t.Parallel()

	local := newRoutingPipeline()
	local.resolveFromProvenance("llm", "agent.local", 0.88, "play_music")
	if got := local.renderResolved(); !strings.Contains(got, "local-LLM · agent.local") || !strings.Contains(got, "play_music") {
		t.Errorf("local provenance should win the local-LLM layer and name the backend; got %q", got)
	}

	cloud := newRoutingPipeline()
	cloud.resolveFromProvenance("", "", 0, "read_email")
	if got := cloud.renderResolved(); !strings.Contains(got, "routed via main-LLM") || !strings.Contains(got, "claude") {
		t.Errorf("empty provenance (main-turn) should win the main-LLM layer, labelled claude; got %q", got)
	}

	det := newRoutingPipeline()
	det.resolveFromProvenance("deterministic", "", 0, "lamp_on")
	if got := det.renderResolved(); !strings.Contains(got, "routed via deterministic") || strings.Contains(got, "claude") {
		t.Errorf("deterministic provenance should not mention claude; got %q", got)
	}
}

func TestRoutingPipeline_ImportedIntentLabelIsHumanized(t *testing.T) {
	t.Parallel()
	p := newRoutingPipeline()
	p.markHit(TierSemantic, "core__go_dogfood_marathon", "", 0.90, true)

	got := p.renderResolved()
	if strings.Contains(got, "core__") {
		t.Fatalf("resolved line leaked internal imported intent: %q", got)
	}
	if !strings.Contains(got, "go dogfood marathon") {
		t.Fatalf("resolved line = %q, want humanized imported intent label", got)
	}
}

// TestRoutingPipeline_ZeroValueSafe guards the panic that bit the warp path: a
// zero-value pipeline must render without indexing a nil layer slice.
func TestRoutingPipeline_ZeroValueSafe(t *testing.T) {
	t.Parallel()
	var p routingPipeline
	if p.resolved() {
		t.Error("zero-value pipeline must not report resolved")
	}
	_ = p.renderResolved() // must not panic
	_ = p.renderProgress() // must not panic
}
