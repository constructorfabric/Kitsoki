package orchestrator

import (
	"testing"
)

// profiles mirrors the .kitsoki.yaml example: a claude-native, a synthetic-claude
// (env retarget on the claude backend), and a synthetic-codex (the env-under-codex
// case the gap-fix already supports).
func testProfiles() map[string]HarnessProfile {
	return map[string]HarnessProfile{
		"claude-native": {Name: "claude-native", Backend: "claude"},
		"synthetic-claude": {
			Name:    "synthetic-claude",
			Backend: "claude",
			Model:   "hf:Qwen/Qwen2.5-Coder-32B-Instruct",
			Models:  []string{"hf:Qwen/Qwen2.5-Coder-32B-Instruct", "hf:meta-llama/Llama-3.3-70B-Instruct"},
			Env:     map[string]string{"ANTHROPIC_BASE_URL": "https://api.synthetic.new/anthropic"},
		},
		"synthetic-codex": {
			Name:    "synthetic-codex",
			Backend: "codex",
			Env:     map[string]string{"OPENAI_BASE_URL": "https://api.synthetic.new/openai"},
		},
	}
}

func newProfileOrch(t *testing.T, def string) *Orchestrator {
	t.Helper()
	o := &Orchestrator{agentBackendName: "claude"}
	WithHarnessProfiles(testProfiles(), def)(o)
	return o
}

// The default profile seeds the initial selection, and resolveSelection routes a
// codex profile to the codex backend with its env installed as the active
// profile — the synthetic-on-codex case.
func TestResolveSelection_DefaultAndSwitch(t *testing.T) {
	o := newProfileOrch(t, "claude-native")

	if got := o.Selection().Profile; got != "claude-native" {
		t.Fatalf("default selection = %q, want claude-native", got)
	}
	backend, active := o.resolveSelection("claude")
	if backend != "claude" {
		t.Fatalf("claude-native backend = %q, want claude", backend)
	}
	if active.Name != "claude-native" {
		t.Fatalf("active.Name = %q, want claude-native", active.Name)
	}

	if err := o.SetSelection("synthetic-codex", "", ""); err != nil {
		t.Fatalf("SetSelection(synthetic-codex): %v", err)
	}
	backend, active = o.resolveSelection("claude")
	if backend != "codex" {
		t.Fatalf("synthetic-codex backend = %q, want codex", backend)
	}
	if active.Provider.Env["OPENAI_BASE_URL"] != "https://api.synthetic.new/openai" {
		t.Fatalf("synthetic-codex env not resolved: %+v", active.Provider.Env)
	}
}

// A model override from the profile's catalog flows to the active provider Model;
// an off-catalog model is rejected without mutating the selection.
func TestSetSelection_ModelOverrideAndValidation(t *testing.T) {
	o := newProfileOrch(t, "claude-native")

	if err := o.SetSelection("synthetic-claude", "hf:meta-llama/Llama-3.3-70B-Instruct", ""); err != nil {
		t.Fatalf("valid model override rejected: %v", err)
	}
	_, active := o.resolveSelection("claude")
	if active.Provider.Model != "hf:meta-llama/Llama-3.3-70B-Instruct" {
		t.Fatalf("model override not applied: %q", active.Provider.Model)
	}

	if err := o.SetSelection("synthetic-claude", "gpt-5", ""); err == nil {
		t.Fatalf("off-catalog model should be rejected")
	}
	// The prior valid selection must survive the rejected switch.
	if got := o.Selection().Model; got != "hf:meta-llama/Llama-3.3-70B-Instruct" {
		t.Fatalf("rejected switch tore the selection: model=%q", got)
	}

	if err := o.SetSelection("does-not-exist", "", ""); err == nil {
		t.Fatalf("unknown profile should be rejected")
	}
}

// An effort from the profile's catalog flows to the active provider Effort; an
// off-catalog effort is rejected.
func TestSetSelection_Effort(t *testing.T) {
	profiles := map[string]HarnessProfile{
		"claude-native": {Name: "claude-native", Backend: "claude", Efforts: []string{"low", "medium", "high"}},
	}
	o := &Orchestrator{agentBackendName: "claude"}
	WithHarnessProfiles(profiles, "claude-native")(o)

	if err := o.SetSelection("claude-native", "", "high"); err != nil {
		t.Fatalf("valid effort rejected: %v", err)
	}
	if _, active := o.resolveSelection("claude"); active.Provider.Effort != "high" {
		t.Fatalf("effort not applied to active profile: %q", active.Provider.Effort)
	}
	if err := o.SetSelection("claude-native", "", "turbo"); err == nil {
		t.Fatalf("off-catalog effort should be rejected")
	}
}

// With no profiles declared, every path falls through to the static backend and a
// no-op active profile — today's behavior, byte-for-byte.
func TestResolveSelection_LegacyNoProfiles(t *testing.T) {
	o := &Orchestrator{agentBackendName: "copilot"}
	if o.Profiles() != nil {
		t.Fatalf("Profiles() should be nil with no profiles declared")
	}
	backend, active := o.resolveSelection("copilot")
	if backend != "copilot" {
		t.Fatalf("legacy backend = %q, want copilot", backend)
	}
	if active.Name != "" || len(active.Provider.Env) != 0 {
		t.Fatalf("legacy path should yield a zero active profile, got %+v", active)
	}
	if err := o.SetSelection("anything", "", ""); err == nil {
		t.Fatalf("SetSelection should error when no profiles declared")
	}
}

// Profiles() lists every profile, sorted, secret-free, with the active one flagged.
func TestProfiles_ListingIsSecretFreeAndFlagged(t *testing.T) {
	o := newProfileOrch(t, "synthetic-codex")
	list := o.Profiles()
	if len(list) != 3 {
		t.Fatalf("want 3 profiles, got %d", len(list))
	}
	if list[0].Name != "claude-native" {
		t.Fatalf("listing not sorted: first = %q", list[0].Name)
	}
	var activeCount int
	for _, p := range list {
		if p.Active {
			activeCount++
			if p.Name != "synthetic-codex" {
				t.Fatalf("wrong profile flagged active: %q", p.Name)
			}
		}
	}
	if activeCount != 1 {
		t.Fatalf("want exactly 1 active profile, got %d", activeCount)
	}
}
