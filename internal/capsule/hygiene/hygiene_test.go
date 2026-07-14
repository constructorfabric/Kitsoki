package hygiene

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/capsule/control"
)

func TestBuildPlanPrunesOldCIRunSidecarsByRetention(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".capsules", "ci")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRunBundle(t, dir, "old", time.Unix(10, 0))
	writeRunBundle(t, dir, "new", time.Unix(20, 0))

	plan, err := BuildPlan(context.Background(), Options{ProjectRoot: root, KeepRuns: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 3 {
		t.Fatalf("candidates %#v", plan.Candidates)
	}
	for _, c := range plan.Candidates {
		if c.Kind != "ci-run" || !c.Safe || filepath.Base(c.Path)[:3] != "old" {
			t.Fatalf("candidate %#v", c)
		}
	}
}

func TestBuildPlanReturnsEmptyCandidateSlice(t *testing.T) {
	plan, err := BuildPlan(context.Background(), Options{ProjectRoot: t.TempDir(), KeepRuns: 20})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Candidates == nil || len(plan.Candidates) != 0 {
		t.Fatalf("candidates %#v", plan.Candidates)
	}
}

func TestApplyRemovesOnlyPlannedProjectRunFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".capsules", "ci")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRunBundle(t, dir, "old", time.Unix(10, 0))
	writeRunBundle(t, dir, "new", time.Unix(20, 0))

	result, err := Apply(context.Background(), Options{ProjectRoot: root, KeepRuns: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 3 {
		t.Fatalf("removed %#v", result.Removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.run.json")); !os.IsNotExist(err) {
		t.Fatalf("old run still exists or stat failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.run.json")); err != nil {
		t.Fatalf("new run was removed: %v", err)
	}
}

func TestProjectCacheAndGoCacheRequireExplicitInclusion(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, ".capsules", "cache", "runstatus")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, "blob"), []byte("cache"), 0o644); err != nil {
		t.Fatal(err)
	}
	goCache := filepath.Join(t.TempDir(), "gocache")
	if err := os.MkdirAll(goCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goCache, "blob"), []byte("go-cache"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(context.Background(), Options{ProjectRoot: root, GoCachePath: goCache})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 0 {
		t.Fatalf("implicit cache candidates %#v", plan.Candidates)
	}
	plan, err = BuildPlan(context.Background(), Options{ProjectRoot: root, IncludeCapsuleCache: true, IncludeGoBuildCache: true, GoCachePath: goCache})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 2 {
		t.Fatalf("explicit cache candidates %#v", plan.Candidates)
	}
}

func TestApplyUsesInjectedGoCacheCleaner(t *testing.T) {
	root := t.TempDir()
	goCache := filepath.Join(t.TempDir(), "gocache")
	if err := os.MkdirAll(goCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goCache, "blob"), []byte("go-cache"), 0o644); err != nil {
		t.Fatal(err)
	}
	var cleaned bool
	result, err := Apply(context.Background(), Options{ProjectRoot: root, IncludeGoBuildCache: true, GoCachePath: goCache, CleanGoCache: func(context.Context) error {
		cleaned = true
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !cleaned || len(result.Removed) != 1 || result.Removed[0].Kind != "go-build-cache" {
		t.Fatalf("result=%#v cleaned=%v", result, cleaned)
	}
}

func TestBuildPlanInventoriesWorkspaceSafetyGuards(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	old := now.Add(-72 * time.Hour)
	writeManagedWorkspace(t, root, "reclaimable", control.StateIntegrated, old, false)
	writeManagedWorkspace(t, root, "active", control.StateReady, old, false)
	writeManagedWorkspace(t, root, "dirty", control.StateIntegrated, old, true)
	writeManagedWorkspace(t, root, "pinned", control.StateIntegrated, old, false)
	writeManagedWorkspace(t, root, "ownerless", control.StateIntegrated, old, false)
	store := control.FileInstanceStore{Root: filepath.Join(root, ".capsules", "workspaces")}
	ownerless, err := store.Get(context.Background(), "ownerless")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwap(context.Background(), ownerless.ID, ownerless.Generation, func(in *control.Instance) error {
		in.Lease.Owner = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	currentPath := writeManagedWorkspace(t, root, "current", control.StateIntegrated, old, false)
	writeManagedWorkspaceAt(t, root, "staging-local", filepath.Join(root, ".capsules", "staging", "local"), control.StateIntegrated, old, false)
	unknown := filepath.Join(root, ".capsules", "workspaces", "unknown")
	if err := os.MkdirAll(unknown, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknown, "evidence.txt"), []byte("preserve\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:        root,
		KeepWorkspaces:     -1,
		MinWorkspaceAge:    -1,
		PinnedWorkspaceIDs: []string{"pinned"},
		CurrentPath:        currentPath,
		Now:                func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 8 {
		t.Fatalf("candidates=%#v", plan.Candidates)
	}
	assertWorkspaceCandidate(t, plan, "reclaimable", true, "outside retention")
	assertWorkspaceCandidate(t, plan, "active", false, "lifecycle is active")
	assertWorkspaceCandidate(t, plan, "dirty", false, "uncommitted changes")
	assertWorkspaceCandidate(t, plan, "pinned", false, "pinned")
	assertWorkspaceCandidate(t, plan, "ownerless", false, "owner is unknown")
	assertWorkspaceCandidate(t, plan, "current", false, "current process")
	assertWorkspaceCandidate(t, plan, "staging-local", false, "pinned")
	unknownCandidate := assertWorkspaceCandidate(t, plan, "unknown", false, "not present in the manager inventory")
	if unknownCandidate.Managed {
		t.Fatalf("unknown workspace was marked managed: %#v", unknownCandidate)
	}
}

func TestBuildPlanReportsWorkspaceAgeRetentionAndDiskPressure(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	oldPath := writeManagedWorkspace(t, root, "old", control.StateIntegrated, now.Add(-72*time.Hour), false)
	if err := os.WriteFile(filepath.Join(oldPath, ".git", "objects", "shared-clone-data"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManagedWorkspace(t, root, "retained", control.StateIntegrated, now.Add(-48*time.Hour), false)
	writeManagedWorkspace(t, root, "young", control.StateIntegrated, now.Add(-time.Hour), false)

	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:           root,
		KeepWorkspaces:        1,
		MinWorkspaceAge:       24 * time.Hour,
		MeasureWorkspaceBytes: true,
		MinFreeBytes:          150,
		CurrentPath:           root,
		Now:                   func() time.Time { return now },
		ReadDiskUsage: func(string) (DiskUsage, error) {
			return DiskUsage{Known: true, CapacityBytes: 1000, FreeBytes: 100}, nil
		},
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	old := assertWorkspaceCandidate(t, plan, "old", true, "outside retention")
	assertWorkspaceCandidate(t, plan, "retained", false, "retained among newest 1")
	young := assertWorkspaceCandidate(t, plan, "young", false, "younger than minimum cleanup age")
	if old.AgeSeconds != int64((72*time.Hour).Seconds()) || young.AgeSeconds != int64(time.Hour.Seconds()) {
		t.Fatalf("ages old=%d young=%d", old.AgeSeconds, young.AgeSeconds)
	}
	if !plan.Disk.Known || !plan.Disk.BelowMinimum || plan.Disk.MinFreeBytes != 150 || plan.Disk.ProjectedFreeBytes != 100+plan.TotalBytes {
		t.Fatalf("disk=%#v total=%d", plan.Disk, plan.TotalBytes)
	}
	if plan.InventoryBytes < plan.TotalBytes || plan.TotalBytes != old.Bytes {
		t.Fatalf("inventory=%d reclaimable=%d old=%d", plan.InventoryBytes, plan.TotalBytes, old.Bytes)
	}
	if plan.BytesBasis != byteMeasurement || !old.BytesKnown || old.Bytes >= 1<<20 {
		t.Fatalf("bytes basis=%q old workspace estimate=%d", plan.BytesBasis, old.Bytes)
	}
}

func TestBuildPlanDiscoversNestedCapsuleWorkspaceSafetyGuards(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	old := now.Add(-72 * time.Hour)
	child := writeNestedCapsuleProject(t, root, "matrix", "matrix-comparison", old)
	writeNestedManagedWorkspace(t, child, "reclaimable", control.StateIntegrated, old, false)
	writeNestedManagedWorkspace(t, child, "active", control.StateReady, old, false)
	writeNestedManagedWorkspace(t, child, "unmerged", control.StateCommitted, old, false)
	writeNestedManagedWorkspace(t, child, "dirty", control.StateIntegrated, old, true)
	writeNestedManagedWorkspace(t, child, "pinned", control.StateIntegrated, old, false)
	current := writeNestedManagedWorkspace(t, child, "current", control.StateIntegrated, old, false)
	processActive := writeNestedManagedWorkspace(t, child, "process-active", control.StateIntegrated, old, false)
	writeNestedManagedWorkspace(t, child, "recent", control.StateIntegrated, now.Add(-time.Hour), false)

	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:        root,
		KeepWorkspaces:     -1,
		MinWorkspaceAge:    24 * time.Hour,
		PinnedWorkspaceIDs: []string{"pinned"},
		CurrentPath:        current,
		Now:                func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{processActive: {4242}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	reclaimable := assertWorkspaceCandidate(t, plan, "reclaimable", true, "outside retention")
	if reclaimable.CapsuleProject != ".capsules/projects/matrix" || reclaimable.CapsuleProjectKind != "matrix-comparison" || reclaimable.CapsuleProjectManagedBy == "" || !strings.HasPrefix(reclaimable.ProvenanceDigest, "sha256:") {
		t.Fatalf("nested workspace provenance=%#v", reclaimable)
	}
	assertWorkspaceCandidate(t, plan, "active", false, "lifecycle is active")
	assertWorkspaceCandidate(t, plan, "unmerged", false, "committed but unintegrated")
	assertWorkspaceCandidate(t, plan, "dirty", false, "uncommitted changes")
	assertWorkspaceCandidate(t, plan, "pinned", false, "pinned")
	assertWorkspaceCandidate(t, plan, "current", false, "current process")
	active := assertWorkspaceCandidate(t, plan, "process-active", false, "in use by process")
	if fmt.Sprint(active.ActivePIDs) != "[4242]" {
		t.Fatalf("active nested workspace=%#v", active)
	}
	assertWorkspaceCandidate(t, plan, "recent", false, "younger than minimum cleanup age")
	projectCandidate := assertCapsuleProjectCandidate(t, plan, "matrix", false, "current process")
	if !projectCandidate.Managed || projectCandidate.Status != "contains-workspaces" {
		t.Fatalf("nested project candidate=%#v", projectCandidate)
	}
}

func TestBuildPlanProtectsNestedCapsuleProjectRootSafetyGuards(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	old := now.Add(-72 * time.Hour)
	paths := map[string]string{}
	for _, id := range []string{"old", "recent", "pinned", "current", "unknown", "active"} {
		updated := old
		if id == "recent" {
			updated = now.Add(-time.Hour)
		}
		paths[id] = writeNestedCapsuleProject(t, root, id, "generic-tool", updated)
	}
	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:        root,
		KeepWorkspaces:     -1,
		MinWorkspaceAge:    24 * time.Hour,
		PinnedWorkspaceIDs: []string{"pinned"},
		CurrentPath:        paths["current"],
		Now:                func() time.Time { return now },
		ReadWorkspaceActivity: func(_ context.Context, requested []string) (WorkspaceActivity, error) {
			path := requested[0]
			if filepath.Base(path) == "unknown" {
				return WorkspaceActivity{Reason: "probe unavailable"}, nil
			}
			pids := map[string][]int{}
			if filepath.Base(path) == "active" {
				pids[path] = []int{9001}
			}
			return WorkspaceActivity{Known: true, PIDsByPath: pids}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertCapsuleProjectCandidate(t, plan, "old", true, "empty inactive")
	assertCapsuleProjectCandidate(t, plan, "recent", false, "younger than minimum cleanup age")
	assertCapsuleProjectCandidate(t, plan, "pinned", false, "pinned")
	assertCapsuleProjectCandidate(t, plan, "current", false, "current process")
	assertCapsuleProjectCandidate(t, plan, "unknown", false, "process activity is unknown")
	active := assertCapsuleProjectCandidate(t, plan, "active", false, "in use by process")
	if fmt.Sprint(active.ActivePIDs) != "[9001]" {
		t.Fatalf("active nested project=%#v", active)
	}
}

func TestBuildPlanTreatsClosedWorkspaceRecordAsRecentProjectActivity(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	child := writeNestedCapsuleProject(t, root, "recent-close", "generic-tool", now.Add(-72*time.Hour))
	workspaceRoot := filepath.Join(child, ".capsules", "workspaces")
	if _, err := (control.FileInstanceStore{Root: workspaceRoot}).Create(context.Background(), control.Instance{
		ID:           "closed",
		DefinitionID: "self",
		Provider:     "self",
		Path:         filepath.Join(workspaceRoot, "closed"),
		State:        control.StateClosed,
		Lease:        control.Lease{Owner: "owner-closed", Acquired: now.Add(-time.Hour)},
		CreatedAt:    now.Add(-2 * time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: 24 * time.Hour,
		CurrentPath:     root,
		Now:             func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := assertCapsuleProjectCandidate(t, plan, "recent-close", false, "younger than minimum cleanup age")
	if candidate.AgeSeconds != int64(time.Hour.Seconds()) {
		t.Fatalf("candidate=%#v", candidate)
	}
}

func TestBuildPlanInventoriesInvalidNestedCapsuleProjectProvenanceAsUnsafe(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, ".capsules", "projects")
	malformed := filepath.Join(projects, "malformed")
	wrongParent := filepath.Join(projects, "wrong-parent")
	outside := t.TempDir()
	for _, path := range []string{malformed, wrongParent} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(malformed, projectSentinel), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(wrongParent, projectSentinel), capsuleProjectSentinelRecord{Schema: projectSentinelSchema, Kind: "generic", ParentProject: outside, ManagedBy: "test"})
	if err := os.Symlink(outside, filepath.Join(projects, "symlink")); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(context.Background(), Options{ProjectRoot: root, KeepWorkspaces: -1, MinWorkspaceAge: -1})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"malformed", "wrong-parent", "symlink"} {
		candidate := assertCapsuleProjectCandidate(t, plan, id, false, "provenance is invalid")
		if candidate.Managed {
			t.Fatalf("invalid project was marked managed: %#v", candidate)
		}
	}
}

func TestApplyClosesNestedWorkspaceThenRemovesEmptyProjectOnLaterPass(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	child := writeNestedCapsuleProject(t, root, "external", "external-bakeoff", now.Add(-72*time.Hour))
	workspace := writeNestedManagedWorkspace(t, child, "done", control.StateIntegrated, now.Add(-72*time.Hour), false)
	opts := Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: 24 * time.Hour,
		CurrentPath:     root,
		Now:             func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	}
	first, err := Apply(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Removed) != 1 || first.Removed[0].Kind != "workspace" || first.Removed[0].CapsuleProject != ".capsules/projects/external" {
		t.Fatalf("first apply=%#v", first)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("nested workspace remains: %v", err)
	}
	instance, err := (control.FileInstanceStore{Root: filepath.Join(child, ".capsules", "workspaces")}).Get(context.Background(), "done")
	if err != nil || instance.State != control.StateClosed {
		t.Fatalf("closed instance=%#v err=%v", instance, err)
	}
	secondOpts := opts
	secondOpts.MinWorkspaceAge = -1
	second, err := Apply(context.Background(), secondOpts)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Removed) != 1 || second.Removed[0].Kind != "capsule-project" {
		t.Fatalf("second apply=%#v", second)
	}
	if _, err := os.Stat(child); !os.IsNotExist(err) {
		t.Fatalf("empty nested project remains: %v", err)
	}
}

func TestApplyRechecksNestedProjectAndSkipsNewPin(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	child := writeNestedCapsuleProject(t, root, "race", "generic-tool", now.Add(-72*time.Hour))
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: 24 * time.Hour,
		CurrentPath:     root,
		Now:             func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
		BeforeApply: func(candidate Candidate) {
			if candidate.Kind == "capsule-project" {
				if err := os.WriteFile(filepath.Join(child, workspacePinSentinel), []byte("investigating\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 0 || len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "pinned") {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatalf("pinned nested project was removed: %v", err)
	}
}

func TestApplyRechecksNestedProjectAndSkipsNewProcessActivity(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	child := writeNestedCapsuleProject(t, root, "active-race", "generic-tool", now.Add(-72*time.Hour))
	activityChecks := 0
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: 24 * time.Hour,
		CurrentPath:     root,
		Now:             func() time.Time { return now },
		ReadWorkspaceActivity: func(_ context.Context, paths []string) (WorkspaceActivity, error) {
			activityChecks++
			pids := map[string][]int{}
			if activityChecks > 1 {
				pids[child] = []int{7331}
			}
			return WorkspaceActivity{Known: true, PIDsByPath: pids}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if activityChecks != 2 || len(result.Removed) != 0 || len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "in use by process") {
		t.Fatalf("result=%#v activity_checks=%d", result, activityChecks)
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatalf("active nested project was removed: %v", err)
	}
}

func TestApplyRechecksNestedProjectAndSkipsChangedProvenance(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	child := writeNestedCapsuleProject(t, root, "provenance-race", "generic-tool", now.Add(-72*time.Hour))
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: 24 * time.Hour,
		CurrentPath:     root,
		Now:             func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
		BeforeApply: func(candidate Candidate) {
			if candidate.Kind == "capsule-project" {
				writeTestJSON(t, filepath.Join(child, projectSentinel), capsuleProjectSentinelRecord{
					Schema:        projectSentinelSchema,
					Kind:          "generic-tool",
					ParentProject: root,
					ManagedBy:     "different-owner",
				})
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 0 || len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "provenance changed") {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(child); err != nil {
		t.Fatalf("changed nested project was removed: %v", err)
	}
}

func TestApplyRechecksWorkspaceAndSkipsNewDirtyState(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	workspace := writeManagedWorkspace(t, root, "race", control.StateIntegrated, now.Add(-72*time.Hour), false)
	closed := false
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		Now:             func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
		BeforeApply: func(candidate Candidate) {
			if candidate.Kind == "workspace" {
				if writeErr := os.WriteFile(filepath.Join(workspace, "new-work.txt"), []byte("preserve me\n"), 0o644); writeErr != nil {
					t.Fatal(writeErr)
				}
			}
		},
		CloseWorkspace: func(context.Context, string, Candidate) error {
			closed = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed || len(result.Removed) != 0 || len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "uncommitted changes") {
		t.Fatalf("result=%#v closed=%t", result, closed)
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("workspace was removed: %v", err)
	}
}

func TestApplyClosesRecheckedManagedWorkspace(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	workspace := writeManagedWorkspace(t, root, "done", control.StateIntegrated, now.Add(-72*time.Hour), false)
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		Now:             func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
		CloseWorkspace: func(_ context.Context, _ string, candidate Candidate) error {
			if candidate.WorkspaceID != "done" || candidate.Owner != "owner-done" {
				return fmt.Errorf("unexpected close candidate: %#v", candidate)
			}
			return os.RemoveAll(workspace)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || result.Removed[0].WorkspaceID != "done" || len(result.Skipped) != 0 {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace still exists or unexpected stat error: %v", err)
	}
}

func TestApplySkipsNativeWorkspaceThatBecomesProcessActive(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	workspace := writeManagedWorkspace(t, root, "active-race", control.StateIntegrated, now.Add(-72*time.Hour), false)
	activityChecks := 0
	closed := false
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		Now:             func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			activityChecks++
			pids := map[string][]int{}
			if activityChecks > 1 {
				pids[workspace] = []int{4242}
			}
			return WorkspaceActivity{Known: true, PIDsByPath: pids}, nil
		},
		CloseWorkspace: func(context.Context, string, Candidate) error {
			closed = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed || activityChecks != 2 || len(result.Removed) != 0 || len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "in use by process") {
		t.Fatalf("result=%#v activity_checks=%d closed=%t", result, activityChecks, closed)
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("active workspace was removed: %v", err)
	}
}

func TestApplyReportsOneSkipWhenWorkspaceRecordDisappearsDuringRecheck(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	workspace := writeManagedWorkspace(t, root, "missing-record", control.StateIntegrated, now.Add(-72*time.Hour), false)
	closed := false
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		Now:             func() time.Time { return now },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
		BeforeApply: func(candidate Candidate) {
			if candidate.Kind != "workspace" {
				return
			}
			record := filepath.Join(root, ".capsules", "workspaces", ".kitsoki-capsule-instances", candidate.WorkspaceID+".json")
			if removeErr := os.Remove(record); removeErr != nil {
				t.Fatal(removeErr)
			}
		},
		CloseWorkspace: func(context.Context, string, Candidate) error {
			closed = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed || len(result.Removed) != 0 || len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "recheck failed") {
		t.Fatalf("result=%#v closed=%t", result, closed)
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("workspace was removed: %v", err)
	}
}

func TestApplyRetriesAndToleratesConcurrentGoCacheCleanup(t *testing.T) {
	root := t.TempDir()
	goCache := filepath.Join(t.TempDir(), "gocache")
	if err := os.MkdirAll(goCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goCache, "blob"), []byte("go-cache"), 0o644); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	result, err := Apply(context.Background(), Options{
		ProjectRoot:         root,
		IncludeGoBuildCache: true,
		GoCachePath:         goCache,
		CleanGoCache: func(context.Context) error {
			attempts++
			if attempts == 1 {
				return errors.New("directory not empty")
			}
			return nil
		},
	})
	if err != nil || attempts != 2 || len(result.Removed) != 1 || len(result.Tolerated) != 0 {
		t.Fatalf("result=%#v attempts=%d err=%v", result, attempts, err)
	}

	attempts = 0
	result, err = Apply(context.Background(), Options{
		ProjectRoot:         root,
		IncludeGoBuildCache: true,
		GoCachePath:         goCache,
		CleanGoCache: func(context.Context) error {
			attempts++
			return os.ErrNotExist
		},
	})
	if err != nil || attempts != 3 || len(result.Removed) != 0 || len(result.Tolerated) != 1 {
		t.Fatalf("result=%#v attempts=%d err=%v", result, attempts, err)
	}
}

func TestBuildPlanNeverMarksUnknownCIRunStatusSafe(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".capsules", "ci")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRunBundleStatus(t, dir, "running", time.Unix(10, 0), "running")
	plan, err := BuildPlan(context.Background(), Options{ProjectRoot: root, KeepRuns: -1})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 3 {
		t.Fatalf("candidates=%#v", plan.Candidates)
	}
	for _, candidate := range plan.Candidates {
		if candidate.Safe || candidate.Status != "running" {
			t.Fatalf("candidate=%#v", candidate)
		}
	}
}

func TestBuildPlanAdoptsOnlyInactiveCleanMergedLegacyWorkspace(t *testing.T) {
	root := t.TempDir()
	initLegacyProject(t, root)
	created := time.Now().UTC()
	safePath := writeLegacyWorkspace(t, root, "legacy-safe", created, false, false)
	writeLegacyWorkspace(t, root, "legacy-dirty", created, true, false)
	writeLegacyWorkspace(t, root, "legacy-unmerged", created, false, true)
	activePath := writeLegacyWorkspace(t, root, "legacy-active", created, false, false)
	writeLegacyWorkspace(t, root, "legacy-initializing", created, false, false)
	initializing := filepath.Join(root, ".capsules", "workspaces", ".initializing")
	if err := os.MkdirAll(initializing, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(initializing, "legacy-initializing"), []byte("pid=999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	invalidPath := writeLegacyWorkspace(t, root, "legacy-invalid", created, false, false)
	if err := os.WriteFile(filepath.Join(invalidPath, ".kitsoki-clone"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: 24 * time.Hour,
		CurrentPath:     root,
		Now:             func() time.Time { return created.Add(72 * time.Hour) },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{activePath: {4242}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	safe := assertWorkspaceCandidate(t, plan, "legacy-safe", true, "outside retention")
	if !safe.Legacy || !safe.Managed || !safe.Merged || !safe.ActivityKnown || safe.Head == "" || safe.Branch != "agent/legacy-safe" || safe.Target != "staging/local" {
		t.Fatalf("safe legacy candidate=%#v path=%s", safe, safePath)
	}
	assertWorkspaceCandidate(t, plan, "legacy-dirty", false, "uncommitted changes")
	assertWorkspaceCandidate(t, plan, "legacy-unmerged", false, "not contained in declared target")
	assertWorkspaceCandidate(t, plan, "legacy-initializing", false, "initialization marker")
	active := assertWorkspaceCandidate(t, plan, "legacy-active", false, "in use by process")
	if len(active.ActivePIDs) != 1 || active.ActivePIDs[0] != 4242 {
		t.Fatalf("active candidate=%#v", active)
	}
	assertWorkspaceCandidate(t, plan, "legacy-invalid", false, "metadata is invalid")
}

func TestClearInactiveMergedPlansOnlyCooledOffIntegratedOrBranchMergedWorkspaces(t *testing.T) {
	root := t.TempDir()
	initLegacyProject(t, root)
	now := time.Now().UTC()
	writeLegacyWorkspace(t, root, "legacy-merged", now, false, false)
	writeLegacyWorkspace(t, root, "legacy-unmerged", now, false, true)
	writeManagedWorkspace(t, root, "integrated", control.StateIntegrated, now, false)
	writeManagedWorkspace(t, root, "closed", control.StateClosed, now, false)
	ci := filepath.Join(root, ".capsules", "ci")
	if err := os.MkdirAll(ci, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRunBundle(t, ci, "evidence", now)

	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:         root,
		KeepWorkspaces:      -1,
		MinWorkspaceAge:     5 * time.Minute,
		ClearInactiveMerged: true,
		CurrentPath:         root,
		Now:                 func() time.Time { return now.Add(6 * time.Minute) },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Candidates) != 4 {
		t.Fatalf("candidates=%#v", plan.Candidates)
	}
	assertWorkspaceCandidate(t, plan, "legacy-merged", true, "clean merged inactive")
	assertWorkspaceCandidate(t, plan, "legacy-unmerged", false, "not contained")
	assertWorkspaceCandidate(t, plan, "integrated", true, "clean terminal")
	assertWorkspaceCandidate(t, plan, "closed", false, "not integrated")
}

func TestClearInactiveMergedAcceptsLegacyHeadMergedIntoAnotherBranch(t *testing.T) {
	root := t.TempDir()
	initLegacyProject(t, root)
	now := time.Now().UTC()
	workspace := writeLegacyWorkspace(t, root, "merged-elsewhere", now, false, true)
	runHygieneGit(t, root, "fetch", workspace, "agent/merged-elsewhere:refs/heads/recovered")

	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:         root,
		KeepWorkspaces:      -1,
		MinWorkspaceAge:     5 * time.Minute,
		ClearInactiveMerged: true,
		CurrentPath:         root,
		Now:                 func() time.Time { return now.Add(6 * time.Minute) },
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := assertWorkspaceCandidate(t, plan, "merged-elsewhere", true, "clean merged inactive")
	if candidate.Target != "recovered" {
		t.Fatalf("merge target=%q candidate=%#v", candidate.Target, candidate)
	}
}

func TestBuildPlanNeverPrunesLegacyWorkspaceWhenActivityIsUnknown(t *testing.T) {
	root := t.TempDir()
	initLegacyProject(t, root)
	created := time.Now().UTC()
	writeLegacyWorkspace(t, root, "legacy-unknown-activity", created, false, false)
	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Reason: "probe unavailable"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertWorkspaceCandidate(t, plan, "legacy-unknown-activity", false, "process activity is unknown")
}

func TestApplyRechecksLegacyWorkspaceActivityBeforeProviderTeardown(t *testing.T) {
	root := t.TempDir()
	initLegacyProject(t, root)
	created := time.Now().UTC()
	writeLegacyWorkspace(t, root, "legacy-race", created, false, false)
	activityCalls := 0
	closed := false
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		ReadWorkspaceActivity: func(_ context.Context, paths []string) (WorkspaceActivity, error) {
			activityCalls++
			activity := WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}
			if activityCalls > 1 {
				activity.PIDsByPath[paths[0]] = []int{9001}
			}
			return activity, nil
		},
		CloseWorkspace: func(context.Context, string, Candidate) error {
			closed = true
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if closed || activityCalls != 2 || len(result.Removed) != 0 || len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "in use by process") {
		t.Fatalf("result=%#v activity_calls=%d closed=%t", result, activityCalls, closed)
	}
}

func TestApplyUsesLegacyProviderTeardownAfterRecheck(t *testing.T) {
	root := t.TempDir()
	initLegacyProject(t, root)
	created := time.Now().UTC()
	workspace := writeLegacyWorkspace(t, root, "legacy-close", created, false, false)
	script := filepath.Join(root, "scripts", "dev-workspace.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\n[ \"$1\" = teardown ] || exit 11\n[ \"$2\" = --repo ] || exit 12\n[ \"$4\" = --root ] || exit 13\nrm -rf \"$6\"\n"
	if err := os.WriteFile(script, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || !result.Removed[0].Legacy || len(result.Skipped) != 0 {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("legacy workspace still exists or unexpected stat error: %v", err)
	}
}

func TestApplyPurgesClosedWorkspaceQuarantineThroughProvider(t *testing.T) {
	root := t.TempDir()
	initLegacyProject(t, root)
	created := time.Now().UTC()
	workspace := writeLegacyWorkspace(t, root, "closed-finished-20260712", created, false, false)
	script := filepath.Join(root, "scripts", "dev-workspace.sh")
	stub := "#!/bin/sh\n[ \"$1\" = teardown ] || exit 11\n[ \"$2\" = --repo ] || exit 12\n[ \"$4\" = --root ] || exit 13\n[ \"$6\" = --purge-quarantine ] || exit 14\nrm -rf \"$7\"\n"
	if err := os.WriteFile(script, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || !result.Removed[0].Legacy || len(result.Skipped) != 0 {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("closed workspace quarantine still exists or unexpected stat error: %v", err)
	}
}

func TestCloseThenCleanupPreservesTrackedAndIgnoredReviewRoots(t *testing.T) {
	root := t.TempDir()
	runHygieneGit(t, root, "init")
	runHygieneGit(t, root, "config", "user.name", "Close Hygiene Test")
	runHygieneGit(t, root, "config", "user.email", "close-hygiene@example.test")
	for _, dir := range []string{".context", ".artifacts"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		".gitignore":              "/.context/\n/.artifacts/\n",
		"README.md":               "close fixture\n",
		".context/tracked.md":     "tracked context\n",
		".artifacts/tracked.json": "{\"tracked\":true}\n",
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runHygieneGit(t, root, "add", ".gitignore", "README.md")
	runHygieneGit(t, root, "add", "-f", ".context/tracked.md", ".artifacts/tracked.json")
	runHygieneGit(t, root, "commit", "--signoff", "-m", "tracked review roots")
	runHygieneGit(t, root, "branch", "staging/local")

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	scriptSource := filepath.Clean(filepath.Join(wd, "..", "..", "..", "scripts", "dev-workspace.sh"))
	scriptBytes, err := os.ReadFile(scriptSource)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "dev-workspace.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, scriptBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, ".capsules", "workspaces", "tracked-review")
	runHygieneCommand(t, root, script, "create", "--repo", root, "--id", "tracked-review", "--branch", "agent/tracked-review", "--base", "staging/local", "--target", "staging/local", "--no-bootstrap")
	for path, body := range map[string]string{
		".context/ignored.md":                        "ignored context\n",
		".artifacts/ignored.txt":                     "ignored evidence\n",
		".artifacts/issues/future/.provider-payload": "future provider payload\n",
	} {
		full := filepath.Join(workspace, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runHygieneCommand(t, root, script, "close", "--repo", root, "tracked-review")
	closed, err := filepath.Glob(filepath.Join(root, ".capsules", "workspaces", "closed-tracked-review-*"))
	if err != nil || len(closed) != 1 {
		t.Fatalf("closed workspaces=%v err=%v", closed, err)
	}
	quarantine := closed[0]
	for _, path := range []string{".context/tracked.md", ".artifacts/tracked.json", ".context/ignored.md", ".artifacts/ignored.txt", ".artifacts/issues/future/.provider-payload"} {
		if _, err := os.Stat(filepath.Join(quarantine, path)); err != nil {
			t.Fatalf("quarantine lost %s: %v", path, err)
		}
	}
	if status := runHygieneCommand(t, quarantine, "git", "status", "--porcelain", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Fatalf("closed quarantine is tracked-dirty: %s", status)
	}
	preserved, err := filepath.Glob(filepath.Join(root, ".artifacts", "workspace-close", "tracked-review-*", ".artifacts", "issues", "future", ".provider-payload"))
	if err != nil || len(preserved) != 1 {
		t.Fatalf("future provider evidence was not copied: %v err=%v", preserved, err)
	}

	result, err := Apply(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || len(result.Skipped) != 0 {
		t.Fatalf("result=%#v", result)
	}
	if _, err := os.Stat(quarantine); !os.IsNotExist(err) {
		t.Fatalf("closed quarantine still exists or unexpected stat error: %v", err)
	}
}

func TestClosedWorkspaceCannotHideUntrackedFilesWithGitConfig(t *testing.T) {
	root := t.TempDir()
	initLegacyProject(t, root)
	workspace := writeLegacyWorkspace(t, root, "closed-hidden-untracked", time.Now().UTC(), false, false)
	runHygieneGit(t, workspace, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(workspace, "surprise.txt"), []byte("must survive\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := assertWorkspaceCandidate(t, plan, "closed-hidden-untracked", false, "uncommitted changes")
	if !candidate.Dirty {
		t.Fatalf("candidate did not report hidden untracked work: %#v", candidate)
	}
}

func TestRecoveredQuarantineIsEligibleOnlyThroughDurableRefs(t *testing.T) {
	root := t.TempDir()
	initLegacyProject(t, root)
	workspace := writeLegacyWorkspace(t, root, "closed-recovered-safe", time.Now().UTC(), false, false)
	if err := os.WriteFile(filepath.Join(workspace, "recovered.txt"), []byte("recovered work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runHygieneGit(t, workspace, "add", "recovered.txt")
	runHygieneGit(t, workspace, "commit", "--signoff", "-m", "sealed recovered work")
	head := strings.TrimSpace(runHygieneCommand(t, workspace, "git", "rev-parse", "HEAD"))
	tree := strings.TrimSpace(runHygieneCommand(t, workspace, "git", "rev-parse", "HEAD^{tree}"))
	snapshotRef := "refs/kitsoki/dirty-snapshot/" + head
	recoveryRef := "refs/kitsoki/dirty-recovery/" + head
	tipRef := "refs/kitsoki/recovered-quarantine/" + head
	for _, ref := range []string{snapshotRef, recoveryRef, tipRef} {
		runHygieneGit(t, root, "fetch", "--no-tags", workspace, head+":"+ref)
	}
	exclude := filepath.Join(workspace, ".git", "info", "exclude")
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(".kitsoki-recovered-quarantine.json\n"); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(workspace, ".kitsoki-recovered-quarantine.json"), map[string]any{
		"schema":        "kitsoki.recovered-quarantine/v1",
		"snapshot_ref":  snapshotRef,
		"recovery_ref":  recoveryRef,
		"recovery_tree": tree,
		"tip_ref":       tipRef,
		"head":          head,
		"tree":          tree,
	})
	plan, err := BuildPlan(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate := assertWorkspaceCandidate(t, plan, "closed-recovered-safe", true, "clean merged inactive")
	if !candidate.Merged || !candidate.Legacy {
		t.Fatalf("recovered quarantine did not validate as durable managed state: %#v", candidate)
	}

	wrongSnapshotRef := "refs/kitsoki/dirty-snapshot/" + strings.Repeat("a", 40)
	runHygieneGit(t, root, "update-ref", wrongSnapshotRef, head)
	writeTestJSON(t, filepath.Join(workspace, ".kitsoki-recovered-quarantine.json"), map[string]any{
		"schema":        "kitsoki.recovered-quarantine/v1",
		"snapshot_ref":  wrongSnapshotRef,
		"recovery_ref":  recoveryRef,
		"recovery_tree": tree,
		"tip_ref":       tipRef,
		"head":          head,
		"tree":          tree,
	})
	plan, err = BuildPlan(context.Background(), Options{
		ProjectRoot:     root,
		KeepWorkspaces:  -1,
		MinWorkspaceAge: -1,
		CurrentPath:     root,
		ReadWorkspaceActivity: func(context.Context, []string) (WorkspaceActivity, error) {
			return WorkspaceActivity{Known: true, PIDsByPath: map[string][]int{}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertWorkspaceCandidate(t, plan, "closed-recovered-safe", false, "snapshot ref is not content-addressed")
}

func TestParseWorkspaceActivityMapsOpenFilesToWorkspace(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one")
	two := filepath.Join(root, "two")
	for _, path := range []string{one, two} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{filepath.Join(one, "file"), filepath.Join(one, "other")} {
		if err := os.WriteFile(path, []byte("open\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := parseWorkspaceActivity("p22\nn"+filepath.Join(one, "file")+"\np11\nn"+two+"\np22\nn"+filepath.Join(one, "other")+"\n", []string{one, two})
	if fmt.Sprint(got[one]) != "[22]" || fmt.Sprint(got[two]) != "[11]" {
		t.Fatalf("activity=%#v", got)
	}
}

func writeRunBundle(t *testing.T, dir, id string, mod time.Time) {
	t.Helper()
	writeRunBundleStatus(t, dir, id, mod, "done")
}

func writeRunBundleStatus(t *testing.T, dir, id string, mod time.Time, status string) {
	t.Helper()
	for _, suffix := range []string{".run.json", ".receipt.json", ".trace.json"} {
		path := filepath.Join(dir, id+suffix)
		contents := []byte(id + suffix)
		if suffix == ".run.json" {
			contents = []byte(fmt.Sprintf(`{"result":{"job":{"status":%q}}}`, status))
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
}

func writeManagedWorkspace(t *testing.T, root, id string, state control.State, updated time.Time, dirty bool) string {
	t.Helper()
	return writeManagedWorkspaceAt(t, root, id, filepath.Join(root, ".capsules", "workspaces", id), state, updated, dirty)
}

func writeManagedWorkspaceAt(t *testing.T, root, id, workspace string, state control.State, updated time.Time, dirty bool) string {
	t.Helper()
	workspaceRoot := filepath.Join(root, ".capsules", "workspaces")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	runHygieneGit(t, workspace, "init")
	runHygieneGit(t, workspace, "config", "user.name", "Capsule Hygiene Test")
	runHygieneGit(t, workspace, "config", "user.email", "capsule-hygiene@example.test")
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runHygieneGit(t, workspace, "add", "tracked.txt")
	runHygieneGit(t, workspace, "commit", "--signoff", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(workspace, ".git", "info", "exclude"), []byte(".kitsoki-capsule\n.kitsoki-capsule-pin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, workspaceSentinel), []byte("managed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := control.FileInstanceStore{Root: workspaceRoot}
	_, err := store.Create(context.Background(), control.Instance{
		ID:           id,
		DefinitionID: "development",
		Provider:     "development",
		Path:         workspace,
		State:        state,
		Lease:        control.Lease{Owner: "owner-" + id, Acquired: updated},
		CreatedAt:    updated,
		UpdatedAt:    updated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		if err := os.WriteFile(filepath.Join(workspace, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return workspace
}

func writeNestedCapsuleProject(t *testing.T, parentRoot, id, kind string, updated time.Time) string {
	t.Helper()
	root := filepath.Join(parentRoot, ".capsules", "projects", id)
	definition := filepath.Join(root, ".kitsoki", "capsules", "self.yaml")
	if err := os.MkdirAll(filepath.Dir(definition), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(root, projectSentinel), capsuleProjectSentinelRecord{
		Schema:        projectSentinelSchema,
		Kind:          kind,
		ParentProject: parentRoot,
		ManagedBy:     "test/nested-capsule-project",
	})
	writeTestJSON(t, definition, map[string]any{
		"schema": "capsule-definition/v1",
		"id":     "self",
		"source": map[string]any{"kind": "self"},
		"policy": map[string]any{"network": "none"},
	})
	if err := os.Chtimes(filepath.Join(root, projectSentinel), updated, updated); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeNestedManagedWorkspace(t *testing.T, projectRoot, id string, state control.State, updated time.Time, dirty bool) string {
	t.Helper()
	workspaceRoot := filepath.Join(projectRoot, ".capsules", "workspaces")
	workspace := filepath.Join(workspaceRoot, id)
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	runHygieneGit(t, workspace, "init")
	runHygieneGit(t, workspace, "config", "user.name", "Nested Capsule Hygiene Test")
	runHygieneGit(t, workspace, "config", "user.email", "nested-capsule-hygiene@example.test")
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("tracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runHygieneGit(t, workspace, "add", "tracked.txt")
	runHygieneGit(t, workspace, "commit", "--signoff", "-m", "fixture")
	if err := os.WriteFile(filepath.Join(workspace, ".git", "info", "exclude"), []byte("/.kitsoki-capsule\n/.kitsoki-capsule-pin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, workspaceSentinel), []byte(id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (control.FileInstanceStore{Root: workspaceRoot}).Create(context.Background(), control.Instance{
		ID:           id,
		DefinitionID: "self",
		Provider:     "self",
		Path:         workspace,
		State:        state,
		Lease:        control.Lease{Owner: "owner-" + id, Acquired: updated},
		CreatedAt:    updated,
		UpdatedAt:    updated,
	}); err != nil {
		t.Fatal(err)
	}
	if dirty {
		if err := os.WriteFile(filepath.Join(workspace, "dirty.txt"), []byte("preserve\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return workspace
}

func initLegacyProject(t *testing.T, root string) {
	t.Helper()
	runHygieneGit(t, root, "init")
	runHygieneGit(t, root, "config", "user.name", "Legacy Hygiene Test")
	runHygieneGit(t, root, "config", "user.email", "legacy-hygiene@example.test")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("legacy fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runHygieneGit(t, root, "add", "README.md")
	runHygieneGit(t, root, "commit", "--signoff", "-m", "legacy baseline")
	runHygieneGit(t, root, "branch", "staging/local")
	script := filepath.Join(root, "scripts", "dev-workspace.sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeLegacyWorkspace(t *testing.T, root, id string, created time.Time, dirty, unmerged bool) string {
	t.Helper()
	workspaceRoot := filepath.Join(root, ".capsules", "workspaces")
	workspace := filepath.Join(workspaceRoot, id)
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	runHygieneGit(t, root, "clone", "--local", "--origin", "source", root, workspace)
	runHygieneGit(t, workspace, "config", "user.name", "Legacy Hygiene Test")
	runHygieneGit(t, workspace, "config", "user.email", "legacy-hygiene@example.test")
	runHygieneGit(t, workspace, "switch", "-c", "agent/"+id, "source/staging/local")
	if unmerged {
		if err := os.WriteFile(filepath.Join(workspace, "feature.txt"), []byte(id+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runHygieneGit(t, workspace, "add", "feature.txt")
		runHygieneGit(t, workspace, "commit", "--signoff", "-m", "unmerged legacy work")
	}
	if err := os.WriteFile(filepath.Join(workspace, ".git", "info", "exclude"), []byte(".kitsoki-capsule\n.kitsoki-clone\n.kitsoki-dev-workspace.json\ncapsule-manifest.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	branch := "agent/" + id
	clone := legacyCloneManifest{ID: id, Source: root, Root: workspaceRoot, Branch: branch, Base: "staging/local", Target: "staging/local", CreatedAt: created, ManagedBy: "scripts/dev-workspace.sh"}
	dev := map[string]any{
		"id": id, "source": root, "root": workspaceRoot, "branch": branch, "base": "staging/local", "target": "staging/local",
		"created_at": created, "managed_by": "scripts/dev-workspace.sh", "workspace": workspace,
	}
	capsule := map[string]any{
		"capsule_name": "dev-workspace", "workspace": workspace,
		"source":      map[string]any{"repo": root},
		"environment": map[string]any{"kind": "dev-clone", "id": id, "root": workspaceRoot, "target": "staging/local"},
	}
	if err := os.WriteFile(filepath.Join(workspace, workspaceSentinel), []byte("dev-workspace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, filepath.Join(workspace, ".kitsoki-clone"), clone)
	writeTestJSON(t, filepath.Join(workspace, ".kitsoki-dev-workspace.json"), dev)
	writeTestJSON(t, filepath.Join(workspace, "capsule-manifest.json"), capsule)
	if dirty {
		if err := os.WriteFile(filepath.Join(workspace, "dirty.txt"), []byte("preserve\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return workspace
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runHygieneGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func runHygieneCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, out)
	}
	return string(out)
}

func assertWorkspaceCandidate(t *testing.T, plan Plan, id string, safe bool, reason string) Candidate {
	t.Helper()
	for _, candidate := range plan.Candidates {
		if candidate.Kind != "workspace" || candidate.WorkspaceID != id {
			continue
		}
		if candidate.Safe != safe || !strings.Contains(candidate.Reason, reason) {
			t.Fatalf("workspace %q candidate=%#v want safe=%t reason containing %q", id, candidate, safe, reason)
		}
		return candidate
	}
	t.Fatalf("workspace %q missing from %#v", id, plan.Candidates)
	return Candidate{}
}

func assertCapsuleProjectCandidate(t *testing.T, plan Plan, id string, safe bool, reason string) Candidate {
	t.Helper()
	for _, candidate := range plan.Candidates {
		if candidate.Kind != "capsule-project" || candidate.WorkspaceID != id {
			continue
		}
		if candidate.Safe != safe || !strings.Contains(candidate.Reason, reason) {
			t.Fatalf("Capsule project %q candidate=%#v want safe=%t reason containing %q", id, candidate, safe, reason)
		}
		return candidate
	}
	t.Fatalf("Capsule project %q missing from %#v", id, plan.Candidates)
	return Candidate{}
}
