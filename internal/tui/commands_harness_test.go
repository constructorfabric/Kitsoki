package tui

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

func sampleProfiles() []orchestrator.ProfileInfo {
	return []orchestrator.ProfileInfo{
		{Name: "claude-native", Backend: "claude"},
		{Name: "synthetic-claude", Backend: "claude", Model: "hf:Qwen/Qwen2.5-Coder-32B-Instruct",
			Models: []string{"hf:Qwen/Qwen2.5-Coder-32B-Instruct", "hf:meta-llama/Llama-3.3-70B-Instruct"}, Active: true},
		{Name: "synthetic-codex", Backend: "codex"},
	}
}

func sampleHarnessProfiles() map[string]orchestrator.HarnessProfile {
	return map[string]orchestrator.HarnessProfile{
		"claude-native": {Name: "claude-native", Backend: "claude"},
		"synthetic-claude": {
			Name:    "synthetic-claude",
			Backend: "claude",
			Model:   "hf:Qwen/Qwen2.5-Coder-32B-Instruct",
			Models:  []string{"hf:Qwen/Qwen2.5-Coder-32B-Instruct", "hf:meta-llama/Llama-3.3-70B-Instruct"},
			Effort:  "medium",
			Efforts: []string{"low", "medium", "high"},
		},
		"synthetic-codex": {Name: "synthetic-codex", Backend: "codex"},
	}
}

func newHarnessPickerModel(t *testing.T) RootModel {
	t.Helper()
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	if err != nil {
		t.Fatalf("load cloak fixture: %v", err)
	}
	mach, err := machine.New(def)
	if err != nil {
		t.Fatalf("build machine: %v", err)
	}
	mem, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open memory store: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })
	replay, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	if err != nil {
		t.Fatalf("open replay harness: %v", err)
	}
	orch := orchestrator.New(def, mach, mem, replay,
		orchestrator.WithHarnessProfiles(sampleHarnessProfiles(), "synthetic-claude"))
	m := RootModel{orch: orch}
	m.transcript = newTranscriptModel(80, 24)
	return m
}

// renderProviderBlock lists every profile as a numbered menu row and marks the
// active one — the typed blocks.Menu, not hand-rolled strings.
func TestRenderProviderBlock(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)

	out := renderProviderBlock(m, sampleProfiles())
	for _, want := range []string{"1. claude-native", "2. synthetic-claude", "3. synthetic-codex", "(active)", "backend: codex"} {
		if !strings.Contains(out, want) {
			t.Errorf("provider block missing %q\n%s", want, out)
		}
	}
}

// renderModelBlock lists the active profile's catalog and flags the current model.
func TestRenderModelBlock(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)

	active := activeProfile(sampleProfiles())
	out := renderModelBlock(m, active, active.Model)
	if !strings.Contains(out, "hf:Qwen/Qwen2.5-Coder-32B-Instruct") || !strings.Contains(out, "hf:meta-llama/Llama-3.3-70B-Instruct") {
		t.Errorf("model block missing catalog entries\n%s", out)
	}
	// The profile default model is the current one absent a session override.
	if !strings.Contains(out, "(active)") {
		t.Errorf("model block should mark a current model active\n%s", out)
	}
}

// Argument resolution accepts both a 1-based index and an exact name/id, and
// rejects out-of-range / unknown values (which the command turns into an error
// block instead of a SetSelection call).
func TestResolveProfileAndModelArgs(t *testing.T) {
	t.Parallel()
	profiles := sampleProfiles()
	if got := resolveProfileArg(profiles, "2"); got != "synthetic-claude" {
		t.Errorf("index resolve = %q, want synthetic-claude", got)
	}
	if got := resolveProfileArg(profiles, "synthetic-codex"); got != "synthetic-codex" {
		t.Errorf("name resolve = %q, want synthetic-codex", got)
	}
	if got := resolveProfileArg(profiles, "9"); got != "" {
		t.Errorf("out-of-range index should resolve to empty, got %q", got)
	}
	if got := resolveProfileArg(profiles, "nope"); got != "" {
		t.Errorf("unknown name should resolve to empty, got %q", got)
	}

	models := []string{"a", "b", "c"}
	if got := resolveModelArg(models, "3"); got != "c" {
		t.Errorf("model index resolve = %q, want c", got)
	}
	if got := resolveModelArg(models, "b"); got != "b" {
		t.Errorf("model name resolve = %q, want b", got)
	}
	if got := resolveModelArg(models, "z"); got != "" {
		t.Errorf("unknown model should resolve to empty, got %q", got)
	}
}

// activeProfile returns the flagged profile, or a zero value when none is active.
func TestActiveProfile(t *testing.T) {
	t.Parallel()
	if got := activeProfile(sampleProfiles()); got.Name != "synthetic-claude" {
		t.Errorf("activeProfile = %q, want synthetic-claude", got.Name)
	}
	none := []orchestrator.ProfileInfo{{Name: "x"}}
	if got := activeProfile(none); got.Name != "" {
		t.Errorf("activeProfile with none active should be zero, got %q", got.Name)
	}
}

func TestHarnessPickerFollowUpNumberSelectsProvider(t *testing.T) {
	m := newHarnessPickerModel(t)

	updated, cmd := m.handleSlashCommand("/provider")
	if cmd != nil {
		t.Fatalf("/provider should not return async command")
	}
	m = updated.(RootModel)
	if m.pendingHarnessPick != "provider" {
		t.Fatalf("pendingHarnessPick = %q, want provider", m.pendingHarnessPick)
	}

	updated, cmd = m.dispatchInput("3")
	if cmd != nil {
		t.Fatalf("provider follow-up number must not start semantic/LLM routing")
	}
	m = updated.(RootModel)
	if got := m.orch.Selection().Profile; got != "synthetic-codex" {
		t.Fatalf("selected profile = %q, want synthetic-codex", got)
	}
	if m.pendingHarnessPick != "" {
		t.Fatalf("pendingHarnessPick should clear after selection, got %q", m.pendingHarnessPick)
	}
}

func TestHarnessPickerFollowUpNumberSelectsModel(t *testing.T) {
	m := newHarnessPickerModel(t)

	updated, cmd := m.handleSlashCommand("/model")
	if cmd != nil {
		t.Fatalf("/model should not return async command")
	}
	m = updated.(RootModel)
	if m.pendingHarnessPick != "model" {
		t.Fatalf("pendingHarnessPick = %q, want model", m.pendingHarnessPick)
	}

	updated, cmd = m.dispatchInput("2")
	if cmd != nil {
		t.Fatalf("model follow-up number must not start semantic/LLM routing")
	}
	m = updated.(RootModel)
	if got := m.orch.Selection().Model; got != "hf:meta-llama/Llama-3.3-70B-Instruct" {
		t.Fatalf("selected model = %q, want hf:meta-llama/Llama-3.3-70B-Instruct", got)
	}
}

func TestHarnessPickerFollowUpNumberSelectsEffort(t *testing.T) {
	m := newHarnessPickerModel(t)

	updated, cmd := m.handleSlashCommand("/effort")
	if cmd != nil {
		t.Fatalf("/effort should not return async command")
	}
	m = updated.(RootModel)
	if m.pendingHarnessPick != "effort" {
		t.Fatalf("pendingHarnessPick = %q, want effort", m.pendingHarnessPick)
	}

	updated, cmd = m.dispatchInput("3")
	if cmd != nil {
		t.Fatalf("effort follow-up number must not start semantic/LLM routing")
	}
	m = updated.(RootModel)
	if got := m.orch.Selection().Effort; got != "high" {
		t.Fatalf("selected effort = %q, want high", got)
	}
}
