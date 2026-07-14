package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"kitsoki/internal/app"
)

func TestBugfixAgentsCarryExternalWritePolicy(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	def, err := app.Load(filepath.Join(repoRoot, "stories", "bugfix", "app.yaml"))
	if err != nil {
		t.Fatalf("load bugfix story: %v", err)
	}

	wantAgents := []string{
		"reproducer",
		"triager",
		"proposer",
		"implementer",
		"test_author",
		"validator",
		"judge",
	}
	wantPhrases := []string{
		"External-write policy",
		"do not create, comment on, close, transition, or otherwise mutate external tickets",
		"do not use raw gh or other GitHub CLIs",
		"Return artifacts only",
		"story done-room owns ticket comments and close-out through native host.gh.ticket/kitsoki gitops orchestration",
	}

	for _, name := range wantAgents {
		agent, ok := def.Agents[name]
		if !ok {
			t.Fatalf("agent %q missing from bugfix story", name)
		}
		for _, phrase := range wantPhrases {
			if !strings.Contains(agent.SystemPrompt, phrase) {
				t.Errorf("agent %q system prompt missing policy phrase %q", name, phrase)
			}
		}
	}
}

func TestMCPDriverGuardsAgainstMechanicalBugfixWaste(t *testing.T) {
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	body, err := os.ReadFile(filepath.Join(repoRoot, ".agents", "agents", "kitsoki-mcp-driver.md"))
	if err != nil {
		t.Fatalf("read kitsoki-mcp-driver.md: %v", err)
	}
	text := string(body)

	wantPhrases := []string{
		"Token-budget guardrails",
		"Do not call `worktree.list` just to find one bugfix workspace or owner marker.",
		"scripts/dev-workspace.sh status <id>",
		"Do not spin on bare `session.status` polls",
		"host.run {cmd:\"sleep 10\"}",
		"session.trace {since:<previous_last_turn>",
		"After `start`, confirm the reproduce phase verified RED with targeted reads",
		"session.world {key:\"bug_verified\"}",
	}
	for _, phrase := range wantPhrases {
		if !strings.Contains(text, phrase) {
			t.Errorf("driver brief missing mechanical-waste guardrail %q", phrase)
		}
	}
}
