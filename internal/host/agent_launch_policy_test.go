package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/capsule"
	"kitsoki/internal/capsuletest"
	"kitsoki/internal/host"
)

func TestAgentLaunchPolicy_AllowsOpenedCapsuleMainBranch(t *testing.T) {
	t.Parallel()
	dir := capsuletest.Open(t, "clean-repo")
	policy := host.AgentLaunchPolicy{
		Enabled:           true,
		RequireCapsule:    true,
		ProtectedBranches: []string{"main"},
	}

	decision, err := policy.Check(context.Background(), "task", "worker", dir)
	if err != nil {
		t.Fatalf("expected capsule launch to be allowed: %v (decision=%#v)", err, decision)
	}
	if !decision.Allowed {
		t.Fatalf("expected allowed decision, got %#v", decision)
	}
	if decision.CapsuleRoot == "" || decision.CapsuleName != "clean-repo" {
		t.Fatalf("expected capsule metadata, got %#v", decision)
	}
	if decision.GitBranch != "main" {
		t.Fatalf("expected clean-repo branch main, got %#v", decision)
	}
}

func TestAgentLaunchPolicy_DeniesNonCapsuleWhenRequired(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	policy := host.AgentLaunchPolicy{
		Enabled:           true,
		RequireCapsule:    true,
		ProtectedBranches: []string{"release"},
	}

	decision, err := policy.Check(context.Background(), "task", "worker", dir)
	if err == nil {
		t.Fatalf("expected non-capsule denial, got decision %#v", decision)
	}
	if decision.Allowed {
		t.Fatalf("expected denied decision, got %#v", decision)
	}
	if !strings.Contains(err.Error(), "not inside an opened Kitsoki capsule") {
		t.Fatalf("expected capsule denial, got %v", err)
	}
}

func TestAgentLaunchPolicy_DeniesProtectedBranchOutsideCapsule(t *testing.T) {
	t.Parallel()
	dir := capsuletest.Open(t, "clean-repo")
	if err := os.Remove(filepath.Join(dir, capsule.SentinelFile)); err != nil {
		t.Fatalf("remove capsule sentinel: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, capsule.ManifestFile)); err != nil {
		t.Fatalf("remove capsule manifest: %v", err)
	}
	policy := host.AgentLaunchPolicy{
		Enabled:           true,
		ProtectedBranches: []string{"main"},
	}

	decision, err := policy.Check(context.Background(), "task", "worker", dir)
	if err == nil {
		t.Fatalf("expected protected branch denial, got decision %#v", decision)
	}
	if decision.ProtectedBranch != "main" {
		t.Fatalf("expected protected branch main, got %#v", decision)
	}
}

func TestAgentLaunchPolicy_DeniesProtectedRootUnlessAllowedRootCarvesOut(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	allowed := filepath.Join(root, ".worktrees", "capsules")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatalf("mkdir allowed root: %v", err)
	}
	policy := host.AgentLaunchPolicy{
		Enabled:           true,
		ProtectedBranches: []string{"release"},
		ProtectedRoots:    []string{root},
	}
	decision, err := policy.Check(context.Background(), "task", "worker", allowed)
	if err == nil {
		t.Fatalf("expected protected root denial, got decision %#v", decision)
	}
	if decision.ProtectedRoot == "" {
		t.Fatalf("expected protected root in decision, got %#v", decision)
	}

	policy.AllowedRoots = []string{allowed}
	decision, err = policy.Check(context.Background(), "task", "worker", allowed)
	if err != nil {
		t.Fatalf("expected allowed root carve-out, got %v (decision=%#v)", err, decision)
	}
	if !decision.Allowed {
		t.Fatalf("expected allowed decision, got %#v", decision)
	}
}
