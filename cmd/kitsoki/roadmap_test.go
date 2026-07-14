package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoadmapLedgerEventIsIdempotent(t *testing.T) {
	ledger := filepath.Join(t.TempDir(), ".artifacts", "roadmap", "progress.yaml")
	args := []string{
		"roadmap", "ledger", "--ledger", ledger, "event",
		"--event", "proposal_published",
		"--item-id", "proposal/roadmap-ledger",
		"--kind", "proposal",
		"--title", "Roadmap ledger",
		"--status", "proposed",
		"--horizon", "next",
		"--proposal", "docs/proposals/roadmap-ledger.md",
		"--check", "proposal=done",
		"--check", "docs=pending",
		"--check", "feature_yaml=pending",
		"--check", "product_site=pending",
		"--check", "rrweb_demo=pending",
		"--source", "test",
		"--json",
	}

	out, err := execRoot(t, args...)
	if err != nil {
		t.Fatalf("roadmap ledger event: %v\n%s", err, out)
	}
	out, err = execRoot(t, args...)
	if err != nil {
		t.Fatalf("roadmap ledger repeated event: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	body := string(raw)
	if got := strings.Count(body, "event: proposal_published"); got != 1 {
		t.Fatalf("expected one idempotent event, got %d:\n%s", got, body)
	}
	for _, want := range []string{
		"schema: kitsoki-roadmap-ledger/v1",
		"id: proposal/roadmap-ledger",
		"proposal: docs/proposals/roadmap-ledger.md",
		"name: docs",
		"status: pending",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("ledger missing %q:\n%s", want, body)
		}
	}
}

func TestRoadmapLedgerCheckRequiresDoneCoverage(t *testing.T) {
	root := t.TempDir()
	ledger := filepath.Join(root, ".artifacts", "roadmap", "progress.yaml")
	for _, p := range []string{
		"docs/proposals/roadmap-ledger.md",
		"docs/workflows/roadmap-ledger.md",
		"features/roadmap-ledger.yaml",
		"tools/site/src/guide/workflows/roadmap-ledger.md",
		"docs/decks/assets/roadmap-ledger/demo.rrweb.json",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	out, err := execRoot(t,
		"roadmap", "ledger", "--ledger", ledger, "event",
		"--event", "shipped",
		"--item-id", "proposal/roadmap-ledger",
		"--kind", "proposal",
		"--title", "Roadmap ledger",
		"--status", "done",
		"--horizon", "done",
		"--proposal", "docs/proposals/roadmap-ledger.md",
		"--doc", "docs/workflows/roadmap-ledger.md",
		"--feature-yaml", "features/roadmap-ledger.yaml",
		"--product-site", "tools/site/src/guide/workflows/roadmap-ledger.md",
		"--rrweb-demo", "docs/decks/assets/roadmap-ledger/demo.rrweb.json",
		"--check", "proposal=done",
		"--check", "docs=done",
		"--check", "feature_yaml=done",
		"--check", "product_site=done",
		"--check", "rrweb_demo=done",
		"--source", "test",
	)
	if err != nil {
		t.Fatalf("roadmap ledger done event: %v\n%s", err, out)
	}
	out, err = execRoot(t, "roadmap", "ledger", "--ledger", ledger, "check", "--repo-root", root)
	if err != nil {
		t.Fatalf("roadmap ledger check must pass with complete done coverage: %v\n%s", err, out)
	}

	out, err = execRoot(t,
		"roadmap", "ledger", "--ledger", filepath.Join(root, "bad.yaml"), "event",
		"--event", "shipped",
		"--item-id", "proposal/bad",
		"--kind", "proposal",
		"--title", "Bad roadmap item",
		"--status", "done",
		"--proposal", "docs/proposals/missing.md",
		"--check", "proposal=done",
		"--source", "test",
	)
	if err == nil {
		t.Fatalf("done event without complete checks must fail; output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "done item must declare docs check") {
		t.Fatalf("expected missing-check error, got %v\n%s", err, out)
	}
}

func TestRoadmapLedgerStrictCheckRequiresKnownCoverage(t *testing.T) {
	root := t.TempDir()
	ledger := filepath.Join(root, ".artifacts", "roadmap", "progress.yaml")
	out, err := execRoot(t,
		"roadmap", "ledger", "--ledger", ledger, "event",
		"--event", "implementation_started",
		"--item-id", "feature/TKT-123",
		"--kind", "feature",
		"--title", "Feature ticket",
		"--status", "in_progress",
		"--horizon", "now",
		"--ref", "issues/features/TKT-123.md",
		"--check", "proposal=not_applicable",
		"--check", "docs=pending",
		"--check", "feature_yaml=pending",
		"--check", "product_site=pending",
		"--check", "rrweb_demo=pending",
		"--source", "test",
	)
	if err != nil {
		t.Fatalf("roadmap ledger event: %v\n%s", err, out)
	}
	out, err = execRoot(t, "roadmap", "ledger", "--ledger", ledger, "check", "--repo-root", root, "--strict")
	if err != nil {
		t.Fatalf("strict check should accept in-progress item with declared coverage: %v\n%s", err, out)
	}

	bad := filepath.Join(root, "bad.yaml")
	if err := os.WriteFile(bad, []byte(`schema: kitsoki-roadmap-ledger/v1
items:
  - id: feature/orphan
    kind: feature
    title: Orphan
    status: in_progress
events: []
`), 0o644); err != nil {
		t.Fatalf("write bad ledger: %v", err)
	}
	out, err = execRoot(t, "roadmap", "ledger", "--ledger", bad, "check", "--repo-root", root, "--strict")
	if err == nil {
		t.Fatalf("strict check should reject orphan item; output:\n%s", out)
	}
	for _, want := range []string{
		"strict mode requires at least one event",
		"strict mode requires proposal check",
		"strict mode requires at least one proposal, doc, surface, demo, or ref path",
	} {
		if !strings.Contains(out+err.Error(), want) {
			t.Fatalf("strict check missing %q; err=%v\n%s", want, err, out)
		}
	}
}
