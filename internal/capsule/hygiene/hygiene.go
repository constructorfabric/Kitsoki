// Package hygiene inventories, plans, and applies conservative local cleanup
// for Capsule workspaces, CI sidecars, and explicitly granted build caches.
package hygiene

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/project"
)

const Schema = "capsule-hygiene-plan/v1"

const (
	defaultKeepRuns       = 20
	defaultKeepWorkspaces = 5
	defaultWorkspaceAge   = 24 * time.Hour
	workspaceSentinel     = ".kitsoki-capsule"
	workspacePinSentinel  = ".kitsoki-capsule-pin"
	projectSentinel       = ".kitsoki-capsule-project"
	projectSentinelSchema = "capsule-project/v1"
	byteMeasurement       = "logical file bytes; workspace .git/objects excluded as shared clone data"
	workspaceInspectors   = 4
)

type Options struct {
	ProjectRoot           string
	KeepRuns              int
	KeepWorkspaces        int
	MinWorkspaceAge       time.Duration
	MinFreeBytes          int64
	MeasureWorkspaceBytes bool
	IncludeCapsuleCache   bool
	IncludeGoBuildCache   bool
	GoCachePath           string
	PinnedWorkspaceIDs    []string
	CurrentPath           string
	CleanGoCache          func(context.Context) error
	ReadDiskUsage         func(string) (DiskUsage, error)
	ReadWorkspaceActivity func(context.Context, []string) (WorkspaceActivity, error)
	CloseWorkspace        func(context.Context, string, Candidate) error
	BeforeApply           func(Candidate)
	Now                   func() time.Time
	// ClearInactiveMerged limits cleanup to workspaces whose work is already
	// integrated: native workspaces must be in the integrated lifecycle state
	// and legacy workspaces must have their HEAD contained in a branch. It is
	// intentionally used by the no-argument operator clear command, not by the
	// broader retention-based hygiene pass.
	ClearInactiveMerged bool
}

type Candidate struct {
	ID                      string        `json:"id"`
	Kind                    string        `json:"kind"`
	Path                    string        `json:"path"`
	Bytes                   int64         `json:"bytes"`
	Reason                  string        `json:"reason"`
	Safe                    bool          `json:"safe"`
	WorkspaceID             string        `json:"workspace_id,omitempty"`
	State                   control.State `json:"state,omitempty"`
	Owner                   string        `json:"owner,omitempty"`
	Generation              uint64        `json:"generation,omitempty"`
	Status                  string        `json:"status,omitempty"`
	AgeSeconds              int64         `json:"age_seconds,omitempty"`
	UpdatedAt               time.Time     `json:"updated_at,omitempty"`
	Managed                 bool          `json:"managed,omitempty"`
	Dirty                   bool          `json:"dirty,omitempty"`
	Current                 bool          `json:"current,omitempty"`
	Pinned                  bool          `json:"pinned,omitempty"`
	BytesKnown              bool          `json:"bytes_known,omitempty"`
	Legacy                  bool          `json:"legacy,omitempty"`
	Merged                  bool          `json:"merged,omitempty"`
	Branch                  string        `json:"branch,omitempty"`
	Target                  string        `json:"target,omitempty"`
	Head                    string        `json:"head,omitempty"`
	Initializing            bool          `json:"initializing,omitempty"`
	ActivityKnown           bool          `json:"activity_known,omitempty"`
	ActivePIDs              []int         `json:"active_pids,omitempty"`
	CapsuleProject          string        `json:"capsule_project,omitempty"`
	CapsuleProjectKind      string        `json:"capsule_project_kind,omitempty"`
	CapsuleProjectManagedBy string        `json:"capsule_project_managed_by,omitempty"`
	ProvenanceDigest        string        `json:"provenance_digest,omitempty"`
}

// WorkspaceActivity is a point-in-time inventory of processes with an open
// file or working directory inside candidate workspaces. Unknown activity is
// never sufficient proof for pruning a legacy workspace.
type WorkspaceActivity struct {
	Known      bool
	PIDsByPath map[string][]int
	Reason     string
}

type DiskUsage struct {
	Known              bool  `json:"known"`
	CapacityBytes      int64 `json:"capacity_bytes,omitempty"`
	FreeBytes          int64 `json:"free_bytes,omitempty"`
	MinFreeBytes       int64 `json:"min_free_bytes,omitempty"`
	BelowMinimum       bool  `json:"below_minimum,omitempty"`
	ProjectedFreeBytes int64 `json:"projected_free_bytes,omitempty"`
}

type Plan struct {
	Schema                 string      `json:"schema"`
	Project                string      `json:"project"`
	KeepRuns               int         `json:"keep_runs"`
	KeepWorkspaces         int         `json:"keep_workspaces"`
	MinWorkspaceAge        string      `json:"min_workspace_age"`
	BytesBasis             string      `json:"bytes_basis"`
	Candidates             []Candidate `json:"candidates"`
	TotalBytes             int64       `json:"total_bytes"`
	InventoryBytes         int64       `json:"inventory_bytes"`
	Unmeasured             int         `json:"unmeasured_candidates,omitempty"`
	WorkspaceBytesMeasured bool        `json:"workspace_bytes_measured"`
	Disk                   DiskUsage   `json:"disk"`
	DiskError              string      `json:"disk_error,omitempty"`
}

type ApplyResult struct {
	Plan       Plan        `json:"plan"`
	Removed    []Candidate `json:"removed"`
	Skipped    []Candidate `json:"skipped,omitempty"`
	Tolerated  []Candidate `json:"tolerated,omitempty"`
	TotalBytes int64       `json:"total_bytes"`
}

func BuildPlan(ctx context.Context, opts Options) (Plan, error) {
	root, err := canonicalRoot(opts.ProjectRoot)
	if err != nil {
		return Plan{}, err
	}
	keepRuns := normalizeRetention(opts.KeepRuns, defaultKeepRuns)
	keepWorkspaces := normalizeRetention(opts.KeepWorkspaces, defaultKeepWorkspaces)
	minAge := normalizeAge(opts.MinWorkspaceAge)
	plan := Plan{
		Schema:                 Schema,
		Project:                root,
		KeepRuns:               keepRuns,
		KeepWorkspaces:         keepWorkspaces,
		MinWorkspaceAge:        minAge.String(),
		BytesBasis:             byteMeasurement,
		WorkspaceBytesMeasured: opts.MeasureWorkspaceBytes,
		Candidates:             []Candidate{},
	}

	nestedProjects, projectCandidates, err := nestedCapsuleProjects(ctx, root, opts, minAge)
	if err != nil {
		return Plan{}, err
	}
	workspaceCandidates, err := workspaceCandidates(ctx, root, nestedProjects, opts, keepWorkspaces, minAge)
	if err != nil {
		return Plan{}, err
	}
	plan.Candidates = append(plan.Candidates, workspaceCandidates...)
	if !opts.ClearInactiveMerged {
		plan.Candidates = append(plan.Candidates, projectCandidates...)
	}
	if opts.ClearInactiveMerged {
		return finishPlan(root, opts, plan)
	}
	runCandidates, err := ciRunCandidates(root, keepRuns)
	if err != nil {
		return Plan{}, err
	}
	plan.Candidates = append(plan.Candidates, runCandidates...)
	if opts.IncludeCapsuleCache {
		cacheCandidates, err := projectCacheCandidates(ctx, root)
		if err != nil {
			return Plan{}, err
		}
		plan.Candidates = append(plan.Candidates, cacheCandidates...)
	}
	if opts.IncludeGoBuildCache {
		goCandidate, err := goCacheCandidate(ctx, opts)
		if err != nil {
			return Plan{}, err
		}
		if goCandidate.Path != "" {
			plan.Candidates = append(plan.Candidates, goCandidate)
		}
	}
	return finishPlan(root, opts, plan)
}

func finishPlan(root string, opts Options, plan Plan) (Plan, error) {
	sort.Slice(plan.Candidates, func(i, j int) bool {
		if plan.Candidates[i].Kind != plan.Candidates[j].Kind {
			return plan.Candidates[i].Kind < plan.Candidates[j].Kind
		}
		return plan.Candidates[i].Path < plan.Candidates[j].Path
	})
	for _, c := range plan.Candidates {
		if c.BytesKnown {
			plan.InventoryBytes += c.Bytes
		} else {
			plan.Unmeasured++
		}
		if c.Safe {
			plan.TotalBytes += c.Bytes
		}
	}
	reader := opts.ReadDiskUsage
	if reader == nil {
		reader = readDiskUsage
	}
	disk, diskErr := reader(root)
	if diskErr != nil {
		plan.DiskError = diskErr.Error()
	} else {
		disk.MinFreeBytes = opts.MinFreeBytes
		disk.BelowMinimum = disk.Known && opts.MinFreeBytes > 0 && disk.FreeBytes < opts.MinFreeBytes
		disk.ProjectedFreeBytes = disk.FreeBytes + plan.TotalBytes
		plan.Disk = disk
	}
	return plan, nil
}

func Apply(ctx context.Context, opts Options) (ApplyResult, error) {
	plan, err := BuildPlan(ctx, opts)
	if err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{Plan: plan, Removed: []Candidate{}, Skipped: []Candidate{}, Tolerated: []Candidate{}}
	for _, candidate := range plan.Candidates {
		if !candidate.Safe {
			continue
		}
		if opts.BeforeApply != nil {
			opts.BeforeApply(candidate)
		}
		switch candidate.Kind {
		case "workspace":
			fresh, safe, recheckErr := recheckWorkspace(ctx, plan.Project, opts, candidate)
			if recheckErr != nil {
				fresh = candidate
				fresh.Safe = false
				fresh.Reason = "workspace recheck failed: " + recheckErr.Error()
				result.Skipped = append(result.Skipped, fresh)
				continue
			}
			if !safe {
				result.Skipped = append(result.Skipped, fresh)
				continue
			}
			closer := opts.CloseWorkspace
			if closer == nil {
				closer = closeWorkspace
			}
			if err := closer(ctx, plan.Project, fresh); err != nil {
				fresh.Safe = false
				fresh.Reason = "workspace close failed: " + err.Error()
				result.Skipped = append(result.Skipped, fresh)
				continue
			}
			candidate = fresh
		case "go-build-cache":
			cleaner := opts.CleanGoCache
			if cleaner == nil {
				cleaner = cleanGoCache
			}
			tolerated, err := cleanGoCacheWithRetry(ctx, cleaner)
			if err != nil {
				return result, err
			}
			if tolerated {
				candidate.Reason = "Go cache cleanup was already in progress; concurrent cleanup was tolerated"
				result.Tolerated = append(result.Tolerated, candidate)
				continue
			}
		case "capsule-project":
			fresh, safe, recheckErr := recheckNestedCapsuleProject(ctx, plan.Project, opts, candidate)
			if recheckErr != nil {
				fresh = candidate
				fresh.Safe = false
				fresh.Reason = "Capsule project recheck failed: " + recheckErr.Error()
				result.Skipped = append(result.Skipped, fresh)
				continue
			}
			if !safe {
				result.Skipped = append(result.Skipped, fresh)
				continue
			}
			if err := removeProjectPath(plan.Project, fresh.Path); err != nil {
				return result, err
			}
			candidate = fresh
		default:
			if err := removeProjectPath(plan.Project, candidate.Path); err != nil {
				return result, err
			}
		}
		result.Removed = append(result.Removed, candidate)
		result.TotalBytes += candidate.Bytes
	}
	return result, nil
}

type nestedCapsuleProject struct {
	Root       string
	Relative   string
	Name       string
	Kind       string
	ManagedBy  string
	Provenance string
}

type capsuleProjectSentinelRecord struct {
	Schema        string `json:"schema"`
	Kind          string `json:"kind"`
	ParentProject string `json:"parent_project"`
	ManagedBy     string `json:"managed_by"`
}

func nestedCapsuleProjects(ctx context.Context, root string, opts Options, minAge time.Duration) ([]nestedCapsuleProject, []Candidate, error) {
	dir := filepath.Join(root, ".capsules", "projects")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	current := opts.CurrentPath
	if current == "" {
		current, _ = os.Getwd()
	}
	pinned := stringSet(opts.PinnedWorkspaceIDs)
	projects := make([]nestedCapsuleProject, 0, len(entries))
	candidates := make([]Candidate, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		relative, relErr := lexicalProjectRelativePath(root, path)
		if relErr != nil {
			return nil, nil, relErr
		}
		project, sentinelInfo, projectErr := readNestedCapsuleProject(root, path)
		if projectErr != nil {
			info, _ := entry.Info()
			candidate := Candidate{ID: "capsule-project:" + entry.Name(), Kind: "capsule-project", Path: relative, WorkspaceID: entry.Name(), Status: "invalid", Reason: "Capsule project provenance is invalid: " + projectErr.Error(), CapsuleProject: relative}
			if info != nil {
				candidate.UpdatedAt = info.ModTime().UTC()
				candidate.Bytes = info.Size()
				candidate.BytesKnown = !entry.IsDir()
				if candidate.UpdatedAt.Before(now) {
					candidate.AgeSeconds = int64(now.Sub(candidate.UpdatedAt).Seconds())
				}
			}
			candidates = append(candidates, candidate)
			continue
		}
		activity, activityErr := workspaceActivity(ctx, opts, []string{project.Root})
		if activityErr != nil {
			activity = WorkspaceActivity{Reason: activityErr.Error()}
		}
		candidate := inspectNestedCapsuleProject(ctx, root, project, sentinelInfo, current, pinned, now, minAge, activity, opts.MeasureWorkspaceBytes)
		projects = append(projects, project)
		candidates = append(candidates, candidate)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Relative < projects[j].Relative })
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	return projects, candidates, nil
}

func readNestedCapsuleProject(parentRoot, path string) (nestedCapsuleProject, os.FileInfo, error) {
	projectsRoot, err := canonicalPath(filepath.Join(parentRoot, ".capsules", "projects"))
	if err != nil {
		return nestedCapsuleProject{}, nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nestedCapsuleProject{}, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nestedCapsuleProject{}, info, fmt.Errorf("entry must be a real directory, not a symlink or file")
	}
	canonical, err := canonicalPath(path)
	if err != nil {
		return nestedCapsuleProject{}, info, err
	}
	relToProjects, err := filepath.Rel(projectsRoot, canonical)
	if err != nil || relToProjects == "." || filepath.IsAbs(relToProjects) || relToProjects == ".." || strings.HasPrefix(relToProjects, ".."+string(filepath.Separator)) || strings.Contains(relToProjects, string(filepath.Separator)) {
		return nestedCapsuleProject{}, info, fmt.Errorf("entry escapes the direct .capsules/projects child scope")
	}
	sentinelPath := filepath.Join(canonical, projectSentinel)
	sentinelInfo, err := os.Lstat(sentinelPath)
	if err != nil {
		return nestedCapsuleProject{}, info, fmt.Errorf("sentinel: %w", err)
	}
	if sentinelInfo.Mode()&os.ModeSymlink != 0 || !sentinelInfo.Mode().IsRegular() {
		return nestedCapsuleProject{}, sentinelInfo, fmt.Errorf("sentinel must be a regular non-symlink file")
	}
	raw, err := os.ReadFile(sentinelPath)
	if err != nil {
		return nestedCapsuleProject{}, sentinelInfo, err
	}
	var sentinel capsuleProjectSentinelRecord
	if err := json.Unmarshal(raw, &sentinel); err != nil {
		return nestedCapsuleProject{}, sentinelInfo, fmt.Errorf("parse sentinel: %w", err)
	}
	if strings.TrimSpace(sentinel.Schema) != projectSentinelSchema {
		return nestedCapsuleProject{}, sentinelInfo, fmt.Errorf("sentinel schema %q, want %q", sentinel.Schema, projectSentinelSchema)
	}
	if strings.TrimSpace(sentinel.Kind) == "" || strings.TrimSpace(sentinel.ManagedBy) == "" {
		return nestedCapsuleProject{}, sentinelInfo, fmt.Errorf("sentinel kind and managed_by are required")
	}
	declaredParent, err := canonicalPath(sentinel.ParentProject)
	if err != nil {
		return nestedCapsuleProject{}, sentinelInfo, fmt.Errorf("sentinel parent_project: %w", err)
	}
	canonicalParent, err := canonicalPath(parentRoot)
	if err != nil {
		return nestedCapsuleProject{}, sentinelInfo, err
	}
	if declaredParent != canonicalParent {
		return nestedCapsuleProject{}, sentinelInfo, fmt.Errorf("sentinel parent_project %q does not match %q", declaredParent, canonicalParent)
	}
	relative, err := projectRelativePath(canonicalParent, canonical)
	if err != nil {
		return nestedCapsuleProject{}, sentinelInfo, err
	}
	digest := sha256.Sum256(raw)
	return nestedCapsuleProject{Root: canonical, Relative: relative, Name: filepath.Base(canonical), Kind: strings.TrimSpace(sentinel.Kind), ManagedBy: strings.TrimSpace(sentinel.ManagedBy), Provenance: "sha256:" + hex.EncodeToString(digest[:])}, sentinelInfo, nil
}

func inspectNestedCapsuleProject(ctx context.Context, parentRoot string, projectRoot nestedCapsuleProject, sentinelInfo os.FileInfo, current string, pinned map[string]bool, now time.Time, minAge time.Duration, activity WorkspaceActivity, measureBytes bool) Candidate {
	updated := sentinelInfo.ModTime().UTC()
	candidate := Candidate{
		ID:                      "capsule-project:" + projectRoot.Name,
		Kind:                    "capsule-project",
		Path:                    projectRoot.Relative,
		WorkspaceID:             projectRoot.Name,
		Status:                  "unknown",
		UpdatedAt:               updated,
		Managed:                 true,
		ActivityKnown:           activity.Known,
		CapsuleProject:          projectRoot.Relative,
		CapsuleProjectKind:      projectRoot.Kind,
		CapsuleProjectManagedBy: projectRoot.ManagedBy,
		ProvenanceDigest:        projectRoot.Provenance,
	}
	workspaceCount, activeRecords, initializing, lastActivity, inventoryErr := nestedCapsuleProjectWorkspaceState(ctx, projectRoot.Root)
	if lastActivity.After(updated) {
		updated = lastActivity
		candidate.UpdatedAt = updated
	}
	candidate.Initializing = initializing
	if measureBytes && workspaceCount == 0 && !initializing {
		if size, err := pathSize(ctx, projectRoot.Root, false); err == nil {
			candidate.Bytes = size
			candidate.BytesKnown = true
		} else {
			candidate.Reason = "Capsule project size inspection failed: " + err.Error()
			return candidate
		}
	}
	if updated.Before(now) {
		candidate.AgeSeconds = int64(now.Sub(updated).Seconds())
	}
	candidate.ActivePIDs = workspaceActivityPIDs(activity, projectRoot.Root)
	candidate.Current = pathContains(projectRoot.Root, current)
	candidate.Pinned = pinned[projectRoot.Name] || pinned[projectRoot.Relative] ||
		fileExists(filepath.Join(projectRoot.Root, workspacePinSentinel)) ||
		fileExists(filepath.Join(parentRoot, ".capsules", "project-pins", projectRoot.Name))
	if workspaceCount > 0 {
		candidate.Status = "contains-workspaces"
	} else {
		candidate.Status = "empty"
	}
	switch {
	case candidate.Current:
		candidate.Reason = "Capsule project contains the current process directory"
	case candidate.Pinned:
		candidate.Reason = "Capsule project is pinned for investigation or reuse"
	case !candidate.ActivityKnown:
		candidate.Reason = "Capsule project process activity is unknown: " + firstNonEmpty(activity.Reason, "activity probe unavailable")
	case len(candidate.ActivePIDs) > 0:
		candidate.Reason = fmt.Sprintf("Capsule project is in use by process(es): %v", candidate.ActivePIDs)
	case inventoryErr != nil:
		candidate.Reason = "Capsule project workspace inventory is invalid: " + inventoryErr.Error()
	case candidate.Initializing:
		candidate.Reason = "Capsule project has initializing workspace state"
	case workspaceCount > 0:
		candidate.Reason = fmt.Sprintf("Capsule project still contains %d workspace(s)", workspaceCount)
	case activeRecords > 0:
		candidate.Reason = fmt.Sprintf("Capsule project still has %d non-closed workspace record(s)", activeRecords)
	case minAge > 0 && now.Sub(updated) < minAge:
		candidate.Reason = "empty Capsule project is younger than minimum cleanup age " + minAge.String()
	default:
		candidate.Safe = true
		candidate.Reason = "empty inactive sentinel-owned Capsule project is outside the cleanup age guard"
	}
	return candidate
}

func nestedCapsuleProjectWorkspaceState(ctx context.Context, root string) (workspaceCount, activeRecords int, initializing bool, lastActivity time.Time, err error) {
	workspaceRoot := filepath.Join(root, ".capsules", "workspaces")
	entries, readErr := os.ReadDir(workspaceRoot)
	if readErr != nil && !os.IsNotExist(readErr) {
		return 0, 0, false, time.Time{}, readErr
	}
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			workspaceCount++
		}
	}
	initializingEntries, initErr := os.ReadDir(filepath.Join(workspaceRoot, ".initializing"))
	if initErr != nil && !os.IsNotExist(initErr) {
		return workspaceCount, 0, false, time.Time{}, initErr
	}
	hasInitializing := len(initializingEntries) > 0
	instances, storeErr := (control.FileInstanceStore{Root: workspaceRoot}).List(ctx)
	if storeErr != nil {
		return workspaceCount, 0, hasInitializing, time.Time{}, storeErr
	}
	for _, instance := range instances {
		if instance.UpdatedAt.After(lastActivity) {
			lastActivity = instance.UpdatedAt.UTC()
		}
		if instance.State != control.StateClosed {
			activeRecords++
		}
	}
	return workspaceCount, activeRecords, hasInitializing, lastActivity, nil
}

func workspaceCandidates(ctx context.Context, root string, nested []nestedCapsuleProject, opts Options, keep int, minAge time.Duration) ([]Candidate, error) {
	out, err := workspaceCandidatesForRoot(ctx, root, opts, minAge)
	if err != nil {
		return nil, err
	}
	for _, child := range nested {
		candidates, childErr := workspaceCandidatesForRoot(ctx, child.Root, opts, minAge)
		if childErr != nil {
			return nil, childErr
		}
		for i := range candidates {
			decorated, decorateErr := decorateNestedWorkspaceCandidate(root, child, candidates[i])
			if decorateErr != nil {
				return nil, decorateErr
			}
			candidates[i] = decorated
		}
		out = append(out, candidates...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	eligible := make([]int, 0, len(out))
	for i := range out {
		if out[i].Safe {
			eligible = append(eligible, i)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		return out[eligible[i]].UpdatedAt.After(out[eligible[j]].UpdatedAt)
	})
	for _, index := range eligible[:minInt(keep, len(eligible))] {
		out[index].Safe = false
		out[index].Reason = fmt.Sprintf("retained among newest %d clean terminal workspace(s)", keep)
	}
	return out, nil
}

func decorateNestedWorkspaceCandidate(parentRoot string, projectRoot nestedCapsuleProject, candidate Candidate) (Candidate, error) {
	absolute := filepath.Join(projectRoot.Root, filepath.FromSlash(candidate.Path))
	relative, err := projectRelativePath(parentRoot, absolute)
	if err != nil {
		return Candidate{}, err
	}
	candidate.Path = relative
	candidate.ID = "workspace:" + projectRoot.Name + ":" + candidate.WorkspaceID
	candidate.CapsuleProject = projectRoot.Relative
	candidate.CapsuleProjectKind = projectRoot.Kind
	candidate.CapsuleProjectManagedBy = projectRoot.ManagedBy
	candidate.ProvenanceDigest = projectRoot.Provenance
	return candidate, nil
}

func workspaceCandidatesForRoot(ctx context.Context, root string, opts Options, minAge time.Duration) ([]Candidate, error) {
	workspaceRoot := filepath.Join(root, ".capsules", "workspaces")
	store := control.FileInstanceStore{Root: workspaceRoot}
	instances, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]control.Instance{}
	for _, in := range instances {
		if strings.TrimSpace(in.Path) == "" {
			continue
		}
		path, pathErr := canonicalPath(in.Path)
		if pathErr != nil {
			return nil, pathErr
		}
		byPath[path] = in
	}
	paths := map[string]struct{}{}
	for path := range byPath {
		paths[path] = struct{}{}
	}
	for _, scanRoot := range []string{workspaceRoot, filepath.Join(root, ".capsules", "staging")} {
		entries, readErr := os.ReadDir(scanRoot)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			path, pathErr := canonicalPath(filepath.Join(scanRoot, entry.Name()))
			if pathErr != nil {
				return nil, pathErr
			}
			paths[path] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	current := opts.CurrentPath
	if current == "" {
		current, _ = os.Getwd()
	}
	pinned := stringSet(opts.PinnedWorkspaceIDs)
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	activity := WorkspaceActivity{Reason: "workspace activity probe was not run"}
	if len(ordered) > 0 {
		activity, err = workspaceActivity(ctx, opts, ordered)
		if err != nil {
			activity = WorkspaceActivity{Reason: err.Error()}
		}
	}
	type inspection struct {
		candidate Candidate
		err       error
	}
	jobs := make(chan string, len(ordered))
	results := make(chan inspection, len(ordered))
	for _, path := range ordered {
		jobs <- path
	}
	close(jobs)
	workers := minInt(workspaceInspectors, len(ordered))
	for worker := 0; worker < workers; worker++ {
		go func() {
			for path := range jobs {
				in, ok := byPath[path]
				candidate, inspectErr := inspectWorkspace(ctx, root, path, maybeInstance(in, ok), current, pinned, now, minAge, activity, opts.MeasureWorkspaceBytes, opts.ClearInactiveMerged)
				results <- inspection{candidate: candidate, err: inspectErr}
			}
		}()
	}
	out := make([]Candidate, 0, len(ordered))
	for range ordered {
		result := <-results
		if os.IsNotExist(result.err) {
			continue
		}
		if result.err != nil {
			return nil, result.err
		}
		out = append(out, result.candidate)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func inspectWorkspace(ctx context.Context, root, path string, in *control.Instance, current string, pinned map[string]bool, now time.Time, minAge time.Duration, activity WorkspaceActivity, measureBytes, clearInactiveMerged bool) (Candidate, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Candidate{}, err
	}
	bytes := int64(0)
	if measureBytes {
		bytes, err = pathSize(ctx, path, true)
		if err != nil {
			return Candidate{}, err
		}
	}
	relative, err := projectRelativePath(root, path)
	if err != nil {
		return Candidate{}, err
	}
	id := filepath.Base(path)
	updated := info.ModTime().UTC()
	candidate := Candidate{ID: "workspace:" + id, Kind: "workspace", Path: relative, Bytes: bytes, BytesKnown: measureBytes, WorkspaceID: id, Status: "unknown", UpdatedAt: updated, ActivityKnown: activity.Known}
	candidate.ActivePIDs = workspaceActivityPIDs(activity, path)
	if in != nil {
		candidate.WorkspaceID = in.ID
		candidate.ID = "workspace:" + in.ID
		candidate.State = in.State
		candidate.Owner = in.Lease.Owner
		candidate.Generation = in.Generation
		if !in.UpdatedAt.IsZero() {
			candidate.UpdatedAt = in.UpdatedAt.UTC()
		}
	}
	_, sentinelErr := os.Stat(filepath.Join(path, workspaceSentinel))
	candidate.Managed = in != nil && sentinelErr == nil
	legacy, legacyRecognized, legacyErr := readLegacyWorkspace(ctx, root, path)
	if in == nil && legacyRecognized {
		candidate.Legacy = true
		candidate.Managed = legacyErr == nil
		candidate.Branch = legacy.Branch
		candidate.Target = legacy.Target
		candidate.Head = legacy.Head
		candidate.Merged = legacy.Merged
		if !legacy.UpdatedAt.IsZero() {
			candidate.UpdatedAt = legacy.UpdatedAt
		}
	}
	if candidate.UpdatedAt.Before(now) {
		candidate.AgeSeconds = int64(now.Sub(candidate.UpdatedAt).Seconds())
	}
	candidate.Initializing = fileExists(filepath.Join(filepath.Dir(path), ".initializing", candidate.WorkspaceID))
	candidate.Current = pathContains(path, current)
	stagingPinned := pathContains(filepath.Join(root, ".capsules", "staging"), path) &&
		!strings.HasPrefix(filepath.Base(path), "closed-")
	candidate.Pinned = pinned[candidate.WorkspaceID] ||
		fileExists(filepath.Join(path, workspacePinSentinel)) ||
		fileExists(filepath.Join(root, ".capsules", "workspace-pins", candidate.WorkspaceID)) ||
		stagingPinned
	status, statusErr := gitStatus(ctx, path)
	if statusErr == nil {
		candidate.Status = "clean"
		candidate.Dirty = strings.TrimSpace(status) != ""
		if candidate.Dirty {
			candidate.Status = "dirty"
		}
	}

	switch {
	case candidate.Current:
		candidate.Reason = "workspace contains the current process directory"
	case candidate.Pinned:
		candidate.Reason = "workspace is pinned for investigation or reuse"
	case !candidate.ActivityKnown:
		candidate.Reason = "workspace process activity is unknown: " + firstNonEmpty(activity.Reason, "activity probe unavailable")
	case len(candidate.ActivePIDs) > 0:
		candidate.Reason = fmt.Sprintf("workspace is in use by process(es): %v", candidate.ActivePIDs)
	case in == nil && !legacyRecognized:
		candidate.Reason = "workspace is not present in the manager inventory"
	case candidate.Legacy && legacyErr != nil:
		candidate.Reason = "legacy workspace metadata is invalid: " + legacyErr.Error()
	case candidate.Initializing:
		candidate.Reason = "workspace has an initialization marker"
	case !candidate.Managed:
		candidate.Reason = "workspace sentinel is missing"
	case !candidate.Legacy && strings.TrimSpace(candidate.Owner) == "":
		candidate.Reason = "workspace lease owner is unknown"
	case !candidate.Legacy && strings.TrimSpace(in.Provider) == "":
		candidate.Reason = "workspace provider is unknown"
	case statusErr != nil:
		candidate.Reason = "workspace Git status is unknown: " + statusErr.Error()
	case candidate.Dirty:
		candidate.Reason = "workspace has uncommitted changes"
	case candidate.Legacy && !candidate.Merged:
		candidate.Reason = "legacy workspace HEAD is not contained in declared target " + candidate.Target
	case clearInactiveMerged && !candidate.Legacy && in.State != control.StateIntegrated:
		candidate.Reason = "workspace is not integrated into a branch"
	case !candidate.Legacy && in.State == control.StateCommitted:
		candidate.Reason = "workspace has committed but unintegrated work"
	case !candidate.Legacy && activeWorkspaceState(in.State):
		candidate.Reason = "workspace lifecycle is active: " + string(in.State)
	case !candidate.Legacy && !terminalWorkspaceState(in.State):
		candidate.Reason = "workspace lifecycle state is unknown: " + string(in.State)
	case minAge > 0 && now.Sub(candidate.UpdatedAt) < minAge:
		candidate.Reason = "terminal workspace is younger than minimum cleanup age " + minAge.String()
	default:
		candidate.Safe = true
		if candidate.Legacy {
			candidate.Reason = "clean merged inactive legacy managed workspace is outside retention and age guards"
		} else {
			candidate.Reason = "clean terminal managed workspace is outside retention and age guards"
		}
	}
	return candidate, nil
}

type legacyWorkspace struct {
	Branch    string
	Target    string
	Head      string
	Merged    bool
	UpdatedAt time.Time
}

type legacyCloneManifest struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Root      string    `json:"root"`
	Branch    string    `json:"branch"`
	Base      string    `json:"base"`
	Target    string    `json:"target"`
	CreatedAt time.Time `json:"created_at"`
	ManagedBy string    `json:"managed_by"`
}

type legacyDevManifest struct {
	legacyCloneManifest
	Workspace string `json:"workspace"`
}

type legacyCapsuleManifest struct {
	CapsuleName string `json:"capsule_name"`
	Workspace   string `json:"workspace"`
	Source      struct {
		Repo string `json:"repo"`
	} `json:"source"`
	Environment struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Root   string `json:"root"`
		Target string `json:"target"`
	} `json:"environment"`
}

type recoveredQuarantineManifest struct {
	Schema       string `json:"schema"`
	SnapshotRef  string `json:"snapshot_ref"`
	RecoveryRef  string `json:"recovery_ref"`
	RecoveryTree string `json:"recovery_tree"`
	TipRef       string `json:"tip_ref"`
	Head         string `json:"head"`
	Tree         string `json:"tree"`
}

func legacyWorkspaceMarker(path string) bool {
	return fileExists(filepath.Join(path, ".kitsoki-clone")) || fileExists(filepath.Join(path, ".kitsoki-dev-workspace.json"))
}

func readLegacyWorkspace(ctx context.Context, root, path string) (legacyWorkspace, bool, error) {
	if !legacyWorkspaceMarker(path) {
		return legacyWorkspace{}, false, nil
	}
	invalid := func(format string, args ...any) (legacyWorkspace, bool, error) {
		return legacyWorkspace{}, true, fmt.Errorf(format, args...)
	}
	sentinel, err := os.ReadFile(filepath.Join(path, workspaceSentinel))
	if err != nil || strings.TrimSpace(string(sentinel)) != "dev-workspace" {
		return invalid("missing dev-workspace capsule sentinel")
	}
	var clone legacyCloneManifest
	if err := readJSON(filepath.Join(path, ".kitsoki-clone"), &clone); err != nil {
		return invalid("clone manifest: %v", err)
	}
	var dev legacyDevManifest
	if err := readJSON(filepath.Join(path, ".kitsoki-dev-workspace.json"), &dev); err != nil {
		return invalid("development manifest: %v", err)
	}
	var capsule legacyCapsuleManifest
	if err := readJSON(filepath.Join(path, "capsule-manifest.json"), &capsule); err != nil {
		return invalid("capsule manifest: %v", err)
	}
	canonicalProject, err := canonicalPath(root)
	if err != nil {
		return invalid("project path: %v", err)
	}
	canonicalWorkspace, err := canonicalPath(path)
	if err != nil {
		return invalid("workspace path: %v", err)
	}
	canonicalWorkspaceRoot, err := canonicalPath(filepath.Dir(path))
	if err != nil {
		return invalid("workspace root: %v", err)
	}
	canonicalCloneSource, sourceErr := canonicalPath(clone.Source)
	canonicalCloneRoot, rootErr := canonicalPath(clone.Root)
	canonicalDevWorkspace, workspaceErr := canonicalPath(dev.Workspace)
	canonicalCapsuleWorkspace, capsuleWorkspaceErr := canonicalPath(capsule.Workspace)
	canonicalCapsuleSource, capsuleSourceErr := canonicalPath(capsule.Source.Repo)
	canonicalCapsuleRoot, capsuleRootErr := canonicalPath(capsule.Environment.Root)
	if sourceErr != nil || rootErr != nil || workspaceErr != nil || capsuleWorkspaceErr != nil || capsuleSourceErr != nil || capsuleRootErr != nil {
		return invalid("manifest contains an invalid path")
	}
	id := filepath.Base(path)
	if clone.ManagedBy != "scripts/dev-workspace.sh" || dev.ManagedBy != clone.ManagedBy ||
		clone.ID != id || dev.ID != id || capsule.Environment.ID != id ||
		canonicalCloneSource != canonicalProject || canonicalCapsuleSource != canonicalProject ||
		canonicalCloneRoot != canonicalWorkspaceRoot || canonicalCapsuleRoot != canonicalWorkspaceRoot ||
		canonicalDevWorkspace != canonicalWorkspace || canonicalCapsuleWorkspace != canonicalWorkspace ||
		clone.Branch == "" || dev.Branch != clone.Branch ||
		clone.Target == "" || dev.Target != clone.Target || capsule.Environment.Target != clone.Target ||
		capsule.CapsuleName != "dev-workspace" || capsule.Environment.Kind != "dev-clone" {
		return invalid("clone, development, and capsule ownership metadata do not agree")
	}
	if info, statErr := os.Stat(filepath.Join(root, "scripts", "dev-workspace.sh")); statErr != nil || !info.Mode().IsRegular() {
		return invalid("legacy teardown provider is unavailable")
	}
	branch, err := gitText(ctx, path, "branch", "--show-current")
	if err != nil || branch != clone.Branch {
		return invalid("checked-out branch does not match manifest branch %q", clone.Branch)
	}
	head, err := gitText(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return invalid("read HEAD: %v", err)
	}
	merged := false
	if strings.HasPrefix(id, "closed-recovered-") {
		if err := validateRecoveredQuarantine(ctx, root, path, head); err != nil {
			return invalid("recovered quarantine: %v", err)
		}
		merged = true
	} else {
		var mergedTarget string
		merged, mergedTarget, err = gitMergedIntoAnyBranch(ctx, root, head, clone.Target)
		if err != nil {
			return invalid("verify branch containment: %v", err)
		}
		if mergedTarget != "" {
			clone.Target = mergedTarget
		}
	}
	updated := workspaceUpdatedAt(ctx, path, clone.Branch, clone.CreatedAt)
	return legacyWorkspace{Branch: clone.Branch, Target: clone.Target, Head: head, Merged: merged, UpdatedAt: updated}, true, nil
}

// gitMergedIntoAnyBranch proves that a legacy capsule can be reconstructed
// from a branch already known to the source repository. Prefer its declared
// target for stable reporting, then accept any local or remote branch that
// contains the exact capsule HEAD.
func gitMergedIntoAnyBranch(ctx context.Context, repo, head, declaredTarget string) (bool, string, error) {
	refs := []string{}
	if strings.TrimSpace(declaredTarget) != "" {
		refs = append(refs, "refs/heads/"+declaredTarget)
	}
	all, err := gitText(ctx, repo, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil {
		return false, "", err
	}
	for _, ref := range strings.Fields(all) {
		if ref == "refs/remotes/origin/HEAD" {
			continue
		}
		seen := false
		for _, candidate := range refs {
			if candidate == ref {
				seen = true
				break
			}
		}
		if !seen {
			refs = append(refs, ref)
		}
	}
	for _, ref := range refs {
		merged, ancestorErr := gitAncestor(ctx, repo, head, ref)
		if ancestorErr != nil {
			return false, "", ancestorErr
		}
		if merged {
			return true, strings.TrimPrefix(ref, "refs/heads/"), nil
		}
	}
	return false, "", nil
}

func validateRecoveredQuarantine(ctx context.Context, root, path, head string) error {
	var marker recoveredQuarantineManifest
	if err := readJSON(filepath.Join(path, ".kitsoki-recovered-quarantine.json"), &marker); err != nil {
		return fmt.Errorf("manifest: %w", err)
	}
	if marker.Schema != "kitsoki.recovered-quarantine/v1" ||
		!strings.HasPrefix(marker.SnapshotRef, "refs/kitsoki/dirty-snapshot/") ||
		!strings.HasPrefix(marker.RecoveryRef, "refs/kitsoki/dirty-recovery/") ||
		!strings.HasPrefix(marker.TipRef, "refs/kitsoki/recovered-quarantine/") ||
		marker.Head == "" || marker.Tree == "" || marker.RecoveryTree == "" {
		return errors.New("ownership fields are invalid")
	}
	if !isObjectID(marker.Head) || !isObjectID(marker.Tree) || !isObjectID(marker.RecoveryTree) {
		return errors.New("ownership object IDs are invalid")
	}
	if marker.Head != head {
		return fmt.Errorf("HEAD changed after seal: got %s want %s", head, marker.Head)
	}
	if marker.Tree != marker.RecoveryTree {
		return fmt.Errorf("sealed tree contains work beyond the signed recovery tree: got %s want %s", marker.Tree, marker.RecoveryTree)
	}
	workspaceTree, err := gitText(ctx, path, "rev-parse", "HEAD^{tree}")
	if err != nil || workspaceTree != marker.Tree {
		return fmt.Errorf("sealed tree changed: got %s want %s", workspaceTree, marker.Tree)
	}
	snapshot, err := gitText(ctx, root, "rev-parse", "--verify", marker.SnapshotRef+"^{commit}")
	if err != nil || snapshot == "" {
		return errors.New("primary snapshot ref is missing or unreadable")
	}
	if strings.TrimPrefix(marker.SnapshotRef, "refs/kitsoki/dirty-snapshot/") != snapshot {
		return fmt.Errorf("primary snapshot ref is not content-addressed to %s", snapshot)
	}
	recovery, err := gitText(ctx, root, "rev-parse", "--verify", marker.RecoveryRef+"^{commit}")
	if err != nil || strings.TrimPrefix(marker.RecoveryRef, "refs/kitsoki/dirty-recovery/") != recovery {
		return fmt.Errorf("primary recovery ref is not content-addressed to %s", recovery)
	}
	recoveryTree, err := gitText(ctx, root, "rev-parse", "--verify", marker.RecoveryRef+"^{tree}")
	if err != nil || recoveryTree != marker.RecoveryTree {
		return fmt.Errorf("primary recovery tree changed: got %s want %s", recoveryTree, marker.RecoveryTree)
	}
	tip, err := gitText(ctx, root, "rev-parse", "--verify", marker.TipRef+"^{commit}")
	if err != nil || tip != marker.Head {
		return fmt.Errorf("primary quarantine tip changed: got %s want %s", tip, marker.Head)
	}
	if strings.TrimPrefix(marker.TipRef, "refs/kitsoki/recovered-quarantine/") != tip {
		return fmt.Errorf("primary quarantine tip ref is not content-addressed to %s", tip)
	}
	return nil
}

func isObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func workspaceUpdatedAt(ctx context.Context, path, branch string, created time.Time) time.Time {
	updated := created.UTC()
	if committed, err := gitText(ctx, path, "show", "-s", "--format=%cI", "HEAD"); err == nil {
		if parsed, parseErr := time.Parse(time.RFC3339, committed); parseErr == nil && parsed.After(updated) {
			updated = parsed.UTC()
		}
	}
	for _, candidate := range []string{
		path,
		filepath.Join(path, ".git", "HEAD"),
		filepath.Join(path, ".git", "index"),
		filepath.Join(path, ".git", "logs", "HEAD"),
		filepath.Join(path, ".git", "refs", "heads", filepath.FromSlash(branch)),
		filepath.Join(path, ".kitsoki-clone"),
		filepath.Join(path, ".kitsoki-dev-workspace.json"),
		filepath.Join(path, "capsule-manifest.json"),
	} {
		if info, err := os.Stat(candidate); err == nil && info.ModTime().After(updated) {
			updated = info.ModTime().UTC()
		}
	}
	return updated
}

func readJSON(path string, value any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, value); err != nil {
		return err
	}
	return nil
}

func gitText(ctx context.Context, path string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = path
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func gitAncestor(ctx context.Context, repo, ancestor, descendant string) (bool, error) {
	if _, err := gitText(ctx, repo, "rev-parse", "--verify", descendant+"^{commit}"); err != nil {
		return false, nil
	}
	if _, err := gitText(ctx, repo, "rev-parse", "--verify", ancestor+"^{commit}"); err != nil {
		return false, nil
	}
	cmd := exec.CommandContext(ctx, "git", "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git merge-base --is-ancestor: %w: %s", err, strings.TrimSpace(string(out)))
}

func workspaceActivity(ctx context.Context, opts Options, paths []string) (WorkspaceActivity, error) {
	reader := opts.ReadWorkspaceActivity
	if reader == nil {
		reader = readWorkspaceActivity
	}
	activity, err := reader(ctx, paths)
	if activity.PIDsByPath == nil {
		activity.PIDsByPath = map[string][]int{}
	}
	return activity, err
}

func readWorkspaceActivity(ctx context.Context, paths []string) (WorkspaceActivity, error) {
	activity := WorkspaceActivity{PIDsByPath: map[string][]int{}}
	lsof, err := exec.LookPath("lsof")
	if err != nil {
		activity.Reason = "lsof is unavailable"
		return activity, nil
	}
	cmd := exec.CommandContext(ctx, lsof, "-n", "-P", "-F", "pn")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	if warning := strings.TrimSpace(stderr.String()); warning != "" {
		return WorkspaceActivity{}, fmt.Errorf("workspace activity probe was incomplete: %s", warning)
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 1 {
			return WorkspaceActivity{}, fmt.Errorf("workspace activity probe: %w", runErr)
		}
	}
	activity.Known = true
	activity.PIDsByPath = parseWorkspaceActivity(stdout.String(), paths)
	return activity, nil
}

func parseWorkspaceActivity(output string, paths []string) map[string][]int {
	sets := map[string]map[int]bool{}
	canonicalWorkspaces := map[string]string{}
	for _, path := range paths {
		sets[path] = map[int]bool{}
		if canonical, err := canonicalPath(path); err == nil {
			canonicalWorkspaces[path] = canonical
		}
	}
	pid := 0
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			openPath := strings.TrimSuffix(line[1:], " (deleted)")
			if pid <= 0 || !filepath.IsAbs(openPath) {
				continue
			}
			canonicalOpen, err := canonicalPath(openPath)
			if err != nil {
				continue
			}
			for workspace, canonicalWorkspace := range canonicalWorkspaces {
				if pathWithin(canonicalWorkspace, canonicalOpen) {
					sets[workspace][pid] = true
				}
			}
		}
	}
	out := map[string][]int{}
	for path, pids := range sets {
		for pid := range pids {
			out[path] = append(out[path], pid)
		}
		sort.Ints(out[path])
	}
	return out
}

func pathWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || (!filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

func workspaceActivityPIDs(activity WorkspaceActivity, path string) []int {
	canonical, err := canonicalPath(path)
	if err != nil {
		return nil
	}
	for candidate, pids := range activity.PIDsByPath {
		candidatePath, candidateErr := canonicalPath(candidate)
		if candidateErr == nil && candidatePath == canonical {
			return append([]int(nil), pids...)
		}
	}
	return nil
}

func recheckWorkspace(ctx context.Context, root string, opts Options, planned Candidate) (Candidate, bool, error) {
	projectRoot, nested, err := candidateCapsuleProjectRoot(root, planned)
	if err != nil {
		return planned, false, err
	}
	if planned.Legacy {
		if nested != nil {
			return planned, false, fmt.Errorf("nested Capsule projects cannot use the legacy workspace provider")
		}
		path := filepath.Join(root, filepath.FromSlash(planned.Path))
		activity, err := workspaceActivity(ctx, opts, []string{path})
		if err != nil {
			activity = WorkspaceActivity{Reason: err.Error()}
		}
		current := opts.CurrentPath
		if current == "" {
			current, _ = os.Getwd()
		}
		now := time.Now().UTC()
		if opts.Now != nil {
			now = opts.Now().UTC()
		}
		fresh, inspectErr := inspectWorkspace(ctx, root, path, nil, current, stringSet(opts.PinnedWorkspaceIDs), now, normalizeAge(opts.MinWorkspaceAge), activity, opts.MeasureWorkspaceBytes, opts.ClearInactiveMerged)
		if inspectErr != nil {
			return planned, false, inspectErr
		}
		return fresh, fresh.Safe, nil
	}
	store := control.FileInstanceStore{Root: filepath.Join(projectRoot, ".capsules", "workspaces")}
	in, err := store.Get(ctx, planned.WorkspaceID)
	if err != nil {
		return planned, false, err
	}
	if in.Generation != planned.Generation || in.State != planned.State || in.Lease.Owner != planned.Owner {
		planned.Safe = false
		planned.Reason = "workspace changed after cleanup planning"
		return planned, false, nil
	}
	actualRelative, err := projectRelativePath(root, in.Path)
	if err != nil || actualRelative != planned.Path {
		planned.Safe = false
		planned.Reason = "workspace path changed after cleanup planning"
		return planned, false, nil
	}
	current := opts.CurrentPath
	if current == "" {
		current, _ = os.Getwd()
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	activity, activityErr := workspaceActivity(ctx, opts, []string{in.Path})
	if activityErr != nil {
		activity = WorkspaceActivity{Reason: activityErr.Error()}
	}
	fresh, err := inspectWorkspace(ctx, projectRoot, in.Path, &in, current, stringSet(opts.PinnedWorkspaceIDs), now, normalizeAge(opts.MinWorkspaceAge), activity, opts.MeasureWorkspaceBytes, opts.ClearInactiveMerged)
	if err != nil {
		return planned, false, err
	}
	if nested != nil {
		fresh, err = decorateNestedWorkspaceCandidate(root, *nested, fresh)
		if err != nil {
			return planned, false, err
		}
	}
	if !fresh.Safe {
		return fresh, false, nil
	}
	return fresh, true, nil
}

func closeWorkspace(ctx context.Context, root string, candidate Candidate) error {
	if candidate.Legacy {
		return closeLegacyWorkspace(ctx, root, candidate)
	}
	projectRoot, _, err := candidateCapsuleProjectRoot(root, candidate)
	if err != nil {
		return err
	}
	manager, err := project.Open(projectRoot, nil)
	if err != nil {
		return err
	}
	return manager.Close(ctx, control.Handle{ID: candidate.WorkspaceID, Generation: candidate.Generation}, candidate.Owner)
}

func candidateCapsuleProjectRoot(parentRoot string, candidate Candidate) (string, *nestedCapsuleProject, error) {
	if strings.TrimSpace(candidate.CapsuleProject) == "" {
		return parentRoot, nil, nil
	}
	path := filepath.Join(parentRoot, filepath.FromSlash(candidate.CapsuleProject))
	projectRoot, _, err := readNestedCapsuleProject(parentRoot, path)
	if err != nil {
		return "", nil, err
	}
	if projectRoot.Relative != candidate.CapsuleProject || projectRoot.Kind != candidate.CapsuleProjectKind || projectRoot.ManagedBy != candidate.CapsuleProjectManagedBy || projectRoot.Provenance != candidate.ProvenanceDigest {
		return "", nil, fmt.Errorf("Capsule project provenance changed after cleanup planning")
	}
	return projectRoot.Root, &projectRoot, nil
}

func recheckNestedCapsuleProject(ctx context.Context, parentRoot string, opts Options, planned Candidate) (Candidate, bool, error) {
	if planned.Kind != "capsule-project" || strings.TrimSpace(planned.CapsuleProject) == "" {
		return planned, false, fmt.Errorf("candidate is not a nested Capsule project")
	}
	path := filepath.Join(parentRoot, filepath.FromSlash(planned.CapsuleProject))
	projectRoot, sentinelInfo, err := readNestedCapsuleProject(parentRoot, path)
	if err != nil {
		return planned, false, err
	}
	if projectRoot.Relative != planned.CapsuleProject || projectRoot.Kind != planned.CapsuleProjectKind || projectRoot.ManagedBy != planned.CapsuleProjectManagedBy || projectRoot.Provenance != planned.ProvenanceDigest {
		planned.Safe = false
		planned.Reason = "Capsule project provenance changed after cleanup planning"
		return planned, false, nil
	}
	current := opts.CurrentPath
	if current == "" {
		current, _ = os.Getwd()
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	activity, activityErr := workspaceActivity(ctx, opts, []string{projectRoot.Root})
	if activityErr != nil {
		activity = WorkspaceActivity{Reason: activityErr.Error()}
	}
	fresh := inspectNestedCapsuleProject(ctx, parentRoot, projectRoot, sentinelInfo, current, stringSet(opts.PinnedWorkspaceIDs), now, normalizeAge(opts.MinWorkspaceAge), activity, opts.MeasureWorkspaceBytes)
	return fresh, fresh.Safe, nil
}

func closeLegacyWorkspace(ctx context.Context, root string, candidate Candidate) error {
	path := filepath.Join(root, filepath.FromSlash(candidate.Path))
	relative, err := projectRelativePath(root, path)
	if err != nil || relative != candidate.Path {
		return fmt.Errorf("capsule hygiene: legacy workspace path failed confinement")
	}
	script := filepath.Join(root, "scripts", "dev-workspace.sh")
	args := []string{"teardown", "--repo", root, "--root", filepath.Dir(path)}
	if strings.HasPrefix(filepath.Base(path), "closed-") {
		args = append(args, "--purge-quarantine")
	}
	args = append(args, path)
	cmd := exec.CommandContext(ctx, script, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("capsule hygiene: legacy provider teardown: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return fmt.Errorf("capsule hygiene: legacy provider teardown returned success but workspace remains")
	}
	return nil
}

type runFile struct {
	job    string
	path   string
	mod    time.Time
	bytes  int64
	status string
}

func ciRunCandidates(root string, keep int) ([]Candidate, error) {
	dir := filepath.Join(root, ".capsules", "ci")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []runFile
	sidecars := map[string][]runFile{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		path := filepath.Join(dir, name)
		job := strings.TrimSuffix(name, ".run.json")
		if job != name {
			runs = append(runs, runFile{job: job, path: path, mod: info.ModTime(), bytes: info.Size(), status: runStatus(path)})
			continue
		}
		for _, suffix := range []string{".receipt.json", ".trace.json"} {
			job = strings.TrimSuffix(name, suffix)
			if job != name {
				sidecars[job] = append(sidecars[job], runFile{job: job, path: path, mod: info.ModTime(), bytes: info.Size()})
				break
			}
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].mod.After(runs[j].mod) })
	if len(runs) <= keep {
		return nil, nil
	}
	var out []Candidate
	for _, run := range runs[keep:] {
		files := append([]runFile{run}, sidecars[run.job]...)
		terminal := terminalRunStatus(run.status)
		for _, file := range files {
			relative, err := projectRelativePath(root, file.path)
			if err != nil {
				return nil, err
			}
			candidate := Candidate{ID: "ci-run:" + run.job + ":" + filepath.Base(file.path), Kind: "ci-run", Path: relative, Bytes: file.bytes, BytesKnown: true, Status: run.status, Safe: terminal}
			if terminal {
				candidate.Reason = fmt.Sprintf("terminal run is older than newest %d Capsule CI run record(s)", keep)
			} else {
				candidate.Reason = "run status is active or unknown: " + firstNonEmpty(run.status, "unknown")
			}
			out = append(out, candidate)
		}
	}
	return out, nil
}

func projectCacheCandidates(ctx context.Context, root string) ([]Candidate, error) {
	dir := filepath.Join(root, ".capsules", "cache")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		bytes, err := pathSize(ctx, path, false)
		if err != nil {
			return nil, err
		}
		relative, err := projectRelativePath(root, path)
		if err != nil {
			return nil, err
		}
		out = append(out, Candidate{ID: "capsule-cache:" + entry.Name(), Kind: "capsule-cache", Path: relative, Bytes: bytes, BytesKnown: true, Reason: "explicit project Capsule cache cleanup requested", Safe: true})
	}
	return out, nil
}

func goCacheCandidate(ctx context.Context, opts Options) (Candidate, error) {
	path := opts.GoCachePath
	if path == "" {
		out, err := exec.CommandContext(ctx, "go", "env", "GOCACHE").Output()
		if err != nil {
			return Candidate{}, fmt.Errorf("capsule hygiene: go env GOCACHE: %w", err)
		}
		path = strings.TrimSpace(string(out))
	}
	if path == "" {
		return Candidate{}, nil
	}
	bytes, err := pathSize(ctx, path, false)
	if err != nil {
		if os.IsNotExist(err) {
			return Candidate{}, nil
		}
		return Candidate{}, err
	}
	return Candidate{ID: "go-build-cache", Kind: "go-build-cache", Path: path, Bytes: bytes, BytesKnown: true, Reason: "explicit Go build/test cache cleanup requested", Safe: true}, nil
}

func removeProjectPath(root, candidatePath string) error {
	path := candidatePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(candidatePath))
	}
	realRoot := root
	if real, err := filepath.EvalSymlinks(root); err == nil {
		realRoot = real
	}
	realPath := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		realPath = real
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("capsule hygiene: refusing to remove path outside project: %s", candidatePath)
	}
	return os.RemoveAll(path)
}

func cleanGoCache(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "go", "clean", "-cache", "-testcache")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("capsule hygiene: go clean: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func cleanGoCacheWithRetry(ctx context.Context, cleaner func(context.Context) error) (bool, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if err := cleaner(ctx); err == nil {
			return false, nil
		} else if !concurrentCacheCleanupError(err) {
			return false, err
		} else {
			last = err
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
	}
	return last != nil, nil
}

func concurrentCacheCleanupError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{"no such file or directory", "directory not empty", "file exists", "resource busy", "being used by another process"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func pathSize(ctx context.Context, path string, excludeSharedGitObjects bool) (int64, error) {
	var total int64
	sharedObjects := filepath.Join(path, ".git", "objects")
	err := filepath.WalkDir(path, func(current string, d os.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if excludeSharedGitObjects && d.IsDir() && filepath.Clean(current) == filepath.Clean(sharedObjects) {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func gitStatus(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = path
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git status: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func runStatus(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var payload struct {
		Result struct {
			Job struct {
				Status string `json:"status"`
			} `json:"job"`
		} `json:"result"`
	}
	if err := jsonUnmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Result.Job.Status)
}

var jsonUnmarshal = func(raw []byte, value any) error {
	return json.Unmarshal(raw, value)
}

func terminalRunStatus(status string) bool {
	switch status {
	case "done", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func activeWorkspaceState(state control.State) bool {
	switch state {
	case control.StateDeclared, control.StateMaterializing, control.StateReady, control.StateDirty, control.StateCommitted, control.StateConflicted:
		return true
	default:
		return false
	}
}

func terminalWorkspaceState(state control.State) bool {
	switch state {
	case control.StateIntegrated, control.StateFailed, control.StateClosed:
		return true
	default:
		return false
	}
}

func normalizeRetention(value, fallback int) int {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return fallback
	}
	return value
}

func normalizeAge(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	if value == 0 {
		return defaultWorkspaceAge
	}
	return value
}

func canonicalRoot(root string) (string, error) {
	return canonicalPath(root)
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return abs, nil
}

func projectRelativePath(root, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	relative, err := filepath.Rel(root, abs)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("capsule hygiene: path is outside project: %s", path)
	}
	return filepath.ToSlash(relative), nil
}

func lexicalProjectRelativePath(root, path string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("capsule hygiene: lexical path is outside project: %s", path)
	}
	return filepath.ToSlash(relative), nil
}

func pathContains(root, path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == "." || (!filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

func maybeInstance(in control.Instance, ok bool) *control.Instance {
	if !ok {
		return nil
	}
	copy := in
	return &copy
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out[value] = true
		}
	}
	return out
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
