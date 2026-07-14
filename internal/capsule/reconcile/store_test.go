package reconcile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFilePlanStoreRejectsTampering(t *testing.T) {
	store := FilePlanStore{ProjectRoot: t.TempDir()}
	plan := Plan{
		ID:        "p",
		Operation: Promote,
		Class:     UpToDate,
		Workspace: "private",
		TargetRef: "main",
		Candidate: "abc",
		Expected:  ObservedRefs{WorkspaceHead: "abc", Target: "abc"},
		CreatedAt: time.Now(),
	}
	plan.Digest = planDigest(plan)
	if err := store.Write(StoredPlan{WorkspaceID: "w", Plan: plan}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(plan.Digest)
	if err != nil || got.WorkspaceID != "w" {
		t.Fatalf("get %#v: %v", got, err)
	}

	path := filepath.Join(store.ProjectRoot, ".capsules", "sync", plan.Digest+".json")
	if err := os.WriteFile(path, []byte(`{"workspace_id":"w","plan":{"digest":"fake"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(plan.Digest); err == nil {
		t.Fatal("Get accepted a tampered plan")
	}
}
