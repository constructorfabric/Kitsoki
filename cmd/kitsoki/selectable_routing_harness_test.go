package main

import (
	"context"
	"testing"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/orchestrator"
)

type selectedHarnessBuild struct {
	harnessType string
	model       string
	backend     string
	active      host.ActiveProfile
}

type selectedHarnessFake struct {
	runCount int
	defs     []*app.AppDef
}

func (f *selectedHarnessFake) RunTurn(context.Context, harness.TurnInput) (mcp.CallToolParams, error) {
	f.runCount++
	return mcp.CallToolParams{}, nil
}

func (f *selectedHarnessFake) Close() error { return nil }

func (f *selectedHarnessFake) SetAppDef(def *app.AppDef) {
	f.defs = append(f.defs, def)
}

func TestSelectableRoutingHarnessUsesCurrentProfileSelection(t *testing.T) {
	t.Parallel()
	var builds []selectedHarnessBuild
	build := func(harnessType, claudeModel, agentBackend, recordingPath, recordPath string, def *app.AppDef, active host.ActiveProfile) (harness.Harness, error) {
		builds = append(builds, selectedHarnessBuild{
			harnessType: harnessType,
			model:       claudeModel,
			backend:     agentBackend,
			active:      active,
		})
		return &selectedHarnessFake{}, nil
	}
	profiles := map[string]orchestrator.HarnessProfile{
		"codex-native": {
			Name:    "codex-native",
			Backend: "codex",
			Model:   "gpt-5.5",
			Effort:  "medium",
			Env: map[string]string{
				"OPENAI_BASE_URL": "https://example.invalid/v1",
			},
		},
	}
	h := newSelectableRoutingHarness("claude", "fallback-model", "claude", "", "", &app.AppDef{}, profiles, "codex-native", build)
	h.SetSelectionResolver(func() orchestrator.ProfileSelection {
		return orchestrator.ProfileSelection{Profile: "codex-native", Model: "gpt-5.5-codex", Effort: "high"}
	})

	if _, err := h.RunTurn(context.Background(), harness.TurnInput{}); err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("build count = %d, want 1", len(builds))
	}
	got := builds[0]
	if got.harnessType != "claude" {
		t.Fatalf("harness type = %q, want claude", got.harnessType)
	}
	if got.backend != "codex" {
		t.Fatalf("backend = %q, want codex", got.backend)
	}
	if got.active.Name != "codex-native" {
		t.Fatalf("active profile = %q, want codex-native", got.active.Name)
	}
	if got.active.Provider.Model != "gpt-5.5-codex" {
		t.Fatalf("active model = %q, want gpt-5.5-codex", got.active.Provider.Model)
	}
	if got.active.Provider.Effort != "high" {
		t.Fatalf("active effort = %q, want high", got.active.Provider.Effort)
	}
	if got.active.Provider.Env["OPENAI_BASE_URL"] != "https://example.invalid/v1" {
		t.Fatalf("active env was not preserved: %#v", got.active.Provider.Env)
	}
}

func TestSelectableRoutingHarnessDefaultProfileBackendUsesCLIHarness(t *testing.T) {
	clearDefaultHarnessProviders(t)
	t.Setenv(host.CodexBinEnv, "/tmp/kitsoki-test-codex")

	profiles := map[string]orchestrator.HarnessProfile{
		"codex-native": {
			Name:    "codex-native",
			Backend: "codex",
			Model:   "gpt-5.5",
		},
	}
	h := newSelectableRoutingHarness("", "", "", "", "", &app.AppDef{}, profiles, "codex-native", nil)

	selected, err := h.selectedHarness()
	if err != nil {
		t.Fatalf("selectedHarness for default codex profile: %v", err)
	}
	if selected == nil {
		t.Fatal("selectedHarness returned nil harness")
	}
	_ = selected.Close()
}

func TestSelectableRoutingHarnessCachesBySelectionAndReloadsCachedHarnesses(t *testing.T) {
	t.Parallel()
	var current orchestrator.ProfileSelection
	var fakes []*selectedHarnessFake
	build := func(harnessType, claudeModel, agentBackend, recordingPath, recordPath string, def *app.AppDef, active host.ActiveProfile) (harness.Harness, error) {
		fake := &selectedHarnessFake{}
		fakes = append(fakes, fake)
		return fake, nil
	}
	profiles := map[string]orchestrator.HarnessProfile{
		"codex-native": {Name: "codex-native", Backend: "codex", Model: "gpt-5.5"},
	}
	h := newSelectableRoutingHarness("claude", "", "claude", "", "", &app.AppDef{}, profiles, "codex-native", build)
	h.SetSelectionResolver(func() orchestrator.ProfileSelection { return current })

	current = orchestrator.ProfileSelection{Profile: "codex-native", Model: "gpt-5.5"}
	if _, err := h.RunTurn(context.Background(), harness.TurnInput{}); err != nil {
		t.Fatalf("first RunTurn: %v", err)
	}
	if _, err := h.RunTurn(context.Background(), harness.TurnInput{}); err != nil {
		t.Fatalf("second RunTurn: %v", err)
	}
	if len(fakes) != 1 {
		t.Fatalf("same selection built %d harnesses, want 1", len(fakes))
	}
	if fakes[0].runCount != 2 {
		t.Fatalf("cached harness run count = %d, want 2", fakes[0].runCount)
	}

	current = orchestrator.ProfileSelection{Profile: "codex-native", Model: "gpt-5.3-codex-spark"}
	if _, err := h.RunTurn(context.Background(), harness.TurnInput{}); err != nil {
		t.Fatalf("third RunTurn: %v", err)
	}
	if len(fakes) != 2 {
		t.Fatalf("changed selection built %d harnesses, want 2", len(fakes))
	}

	nextDef := &app.AppDef{}
	h.SetAppDef(nextDef)
	for i, fake := range fakes {
		if len(fake.defs) != 1 || fake.defs[0] != nextDef {
			t.Fatalf("fake %d defs = %#v, want one reload def", i, fake.defs)
		}
	}
}
