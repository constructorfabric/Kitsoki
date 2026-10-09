package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	capsuletrace "kitsoki/internal/capsule/trace"
)

const ConflictArtifactSchema = "capsule-sync-conflict/v1"
const IntegrationInstanceSchema = "capsule-sync-integration/v1"

type ConflictArtifact struct {
	Schema            string    `json:"schema"`
	PlanDigest        string    `json:"plan_digest"`
	ContinuationToken string    `json:"continuation_token"`
	Operation         Operation `json:"operation"`
	TargetRef         string    `json:"target_ref"`
	Candidate         string    `json:"candidate"`
	Target            string    `json:"target"`
	MergeBase         string    `json:"merge_base"`
	CandidatePaths    []string  `json:"candidate_paths"`
	TargetPaths       []string  `json:"target_paths"`
	OverlapPaths      []string  `json:"overlap_paths"`
	RequiredInputs    []string  `json:"required_inputs"`
}
type IntegrationInstance struct {
	Schema            string    `json:"schema"`
	PlanDigest        string    `json:"plan_digest"`
	ContinuationToken string    `json:"continuation_token"`
	Operation         Operation `json:"operation"`
	TargetRef         string    `json:"target_ref"`
	Candidate         string    `json:"candidate"`
	Target            string    `json:"target"`
	MergeBase         string    `json:"merge_base"`
	InstancePath      string    `json:"instance_path"`
	Branch            string    `json:"branch"`
	State             string    `json:"state"`
	ConflictPaths     []string  `json:"conflict_paths,omitempty"`
	StatusPorcelain   string    `json:"status_porcelain,omitempty"`
}
type ContinuationApplyRequest struct {
	Plan              Plan
	ProjectRoot       string
	ResolverDecision  string
	LostWorkReview    string
	ValidationReceipt string
}
type AbortContinuationRequest struct {
	Plan        Plan
	ProjectRoot string
	Preserve    bool
}
type AbortResult struct {
	PlanDigest        string `json:"plan_digest"`
	ContinuationToken string `json:"continuation_token"`
	Aborted           bool   `json:"aborted"`
	PreservedPatch    string `json:"preserved_patch,omitempty"`
}

func (r Reconciler) MaterializeConflictArtifact(ctx context.Context, p Plan, projectRoot string) (ConflictArtifact, string, error) {
	if err := validateConflictPlan(p); err != nil {
		return ConflictArtifact{}, "", err
	}
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return ConflictArtifact{}, "", err
	}
	mergeBase, err := git(ctx, p.Workspace, "merge-base", p.Candidate, p.Expected.Target)
	if err != nil {
		return ConflictArtifact{}, "", err
	}
	base := strings.TrimSpace(mergeBase)
	candidatePaths, err := changedPaths(ctx, p.Workspace, base, p.Candidate)
	if err != nil {
		return ConflictArtifact{}, "", err
	}
	targetPaths, err := changedPaths(ctx, p.Workspace, base, p.Expected.Target)
	if err != nil {
		return ConflictArtifact{}, "", err
	}
	artifact := ConflictArtifact{
		Schema:            ConflictArtifactSchema,
		PlanDigest:        p.Digest,
		ContinuationToken: p.Continuation.Token,
		Operation:         p.Operation,
		TargetRef:         p.TargetRef,
		Candidate:         p.Candidate,
		Target:            p.Expected.Target,
		MergeBase:         base,
		CandidatePaths:    candidatePaths,
		TargetPaths:       targetPaths,
		OverlapPaths:      overlap(candidatePaths, targetPaths),
		RequiredInputs:    append([]string(nil), p.Continuation.RequiredInputs...),
	}
	path := filepath.Join(root, ".capsules", "sync", p.Continuation.Token+".conflict.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ConflictArtifact{}, "", err
	}
	raw, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return ConflictArtifact{}, "", err
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return ConflictArtifact{}, "", err
	}
	return artifact, path, nil
}

func (r Reconciler) MaterializeIntegrationInstance(ctx context.Context, p Plan, projectRoot string) (IntegrationInstance, string, error) {
	if err := validateConflictPlan(p); err != nil {
		return IntegrationInstance{}, "", err
	}
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return IntegrationInstance{}, "", err
	}
	syncDir := filepath.Join(root, ".capsules", "sync")
	if err := os.MkdirAll(syncDir, 0o755); err != nil {
		return IntegrationInstance{}, "", err
	}
	artifactPath := filepath.Join(syncDir, p.Continuation.Token+".integration.json")
	if raw, err := os.ReadFile(artifactPath); err == nil {
		var existing IntegrationInstance
		if err := json.Unmarshal(raw, &existing); err != nil {
			return IntegrationInstance{}, "", err
		}
		return existing, artifactPath, nil
	} else if !os.IsNotExist(err) {
		return IntegrationInstance{}, "", err
	}
	instancePath := filepath.Join(syncDir, p.Continuation.Token+".integration")
	if _, err := os.Stat(instancePath); err == nil {
		return IntegrationInstance{}, "", fmt.Errorf("capsule reconcile: integration instance already exists without artifact")
	} else if !os.IsNotExist(err) {
		return IntegrationInstance{}, "", err
	}
	if err := gitClone(ctx, p.Workspace, instancePath); err != nil {
		return IntegrationInstance{}, "", err
	}
	if _, err := git(ctx, instancePath, "checkout", "-B", "capsule-sync-resolution", p.Candidate); err != nil {
		return IntegrationInstance{}, "", err
	}
	mergeBase, err := git(ctx, instancePath, "merge-base", p.Candidate, p.Expected.Target)
	if err != nil {
		return IntegrationInstance{}, "", err
	}
	mergeOut, mergeErr := git(ctx, instancePath, "merge", "--no-commit", "--no-ff", p.Expected.Target)
	conflictPaths, conflictErr := unmergedPaths(ctx, instancePath)
	if mergeErr != nil && (conflictErr != nil || len(conflictPaths) == 0) {
		return IntegrationInstance{}, "", fmt.Errorf("%w: %s", mergeErr, strings.TrimSpace(mergeOut))
	}
	status, err := git(ctx, instancePath, "status", "--porcelain")
	if err != nil {
		return IntegrationInstance{}, "", err
	}
	state := "prepared"
	if len(conflictPaths) > 0 {
		state = "conflicted"
	}
	rel, err := filepath.Rel(root, instancePath)
	if err != nil {
		return IntegrationInstance{}, "", err
	}
	instance := IntegrationInstance{
		Schema:            IntegrationInstanceSchema,
		PlanDigest:        p.Digest,
		ContinuationToken: p.Continuation.Token,
		Operation:         p.Operation,
		TargetRef:         p.TargetRef,
		Candidate:         p.Candidate,
		Target:            p.Expected.Target,
		MergeBase:         strings.TrimSpace(mergeBase),
		InstancePath:      filepath.ToSlash(rel),
		Branch:            "capsule-sync-resolution",
		State:             state,
		ConflictPaths:     conflictPaths,
		StatusPorcelain:   strings.TrimSpace(status),
	}
	raw, err := json.MarshalIndent(instance, "", "  ")
	if err != nil {
		return IntegrationInstance{}, "", err
	}
	if err := os.WriteFile(artifactPath, append(raw, '\n'), 0o600); err != nil {
		return IntegrationInstance{}, "", err
	}
	return instance, artifactPath, nil
}

func (r Reconciler) ApplyContinuation(ctx context.Context, req ContinuationApplyRequest) (ApplyResult, error) {
	if r.VCS == nil {
		return ApplyResult{}, fmt.Errorf("capsule reconcile: vcs provider is required")
	}
	p := req.Plan
	if err := validateConflictPlan(p); err != nil {
		return ApplyResult{}, err
	}
	if strings.TrimSpace(req.ResolverDecision) == "" || strings.TrimSpace(req.LostWorkReview) == "" || strings.TrimSpace(req.ValidationReceipt) == "" {
		return ApplyResult{}, fmt.Errorf("capsule reconcile: continuation requires resolver decision, independent lost-work review, and validation receipt")
	}
	root, err := filepath.Abs(req.ProjectRoot)
	if err != nil {
		return ApplyResult{}, err
	}
	instance, err := readIntegrationInstance(root, p.Continuation.Token)
	if err != nil {
		return ApplyResult{}, err
	}
	if instance.PlanDigest != p.Digest || instance.ContinuationToken != p.Continuation.Token {
		return ApplyResult{}, fmt.Errorf("capsule reconcile: integration instance does not match plan")
	}
	instancePath, err := resolveSyncPath(root, instance.InstancePath)
	if err != nil {
		return ApplyResult{}, err
	}
	status, err := git(ctx, instancePath, "status", "--porcelain")
	if err != nil {
		return ApplyResult{}, err
	}
	if strings.TrimSpace(status) != "" {
		return ApplyResult{}, fmt.Errorf("capsule reconcile: integration instance must be clean before continuation apply")
	}
	resolved, err := git(ctx, instancePath, "rev-parse", "HEAD")
	if err != nil {
		return ApplyResult{}, err
	}
	resolved = strings.TrimSpace(resolved)
	candidateIncluded, err := r.VCS.IsAncestor(ctx, instancePath, p.Candidate, resolved)
	if err != nil {
		return ApplyResult{}, err
	}
	targetIncluded, err := r.VCS.IsAncestor(ctx, instancePath, p.Expected.Target, resolved)
	if err != nil {
		return ApplyResult{}, err
	}
	if !candidateIncluded || !targetIncluded {
		return ApplyResult{}, fmt.Errorf("capsule reconcile: resolved integration commit does not preserve both candidate and target histories")
	}
	if p.RequiredGate != "" {
		if r.Gates == nil {
			return ApplyResult{}, fmt.Errorf("capsule reconcile: gate verifier is required")
		}
		if err := r.Gates.Verify(ctx, req.ValidationReceipt, p); err != nil {
			return ApplyResult{}, err
		}
	}
	current, err := r.VCS.Observe(ctx, p.Workspace, p.TargetRef, p.Expected.Generation)
	if err != nil {
		return ApplyResult{}, err
	}
	if current.WorkspaceHead != p.Expected.WorkspaceHead || current.Target != p.Expected.Target || current.Dirty != p.Expected.Dirty || current.Generation != p.Expected.Generation {
		if err := r.emit(ctx, Event{Kind: capsuletrace.KindSyncStale, PlanDigest: p.Digest, Operation: p.Operation, Class: p.Class, TargetRef: p.TargetRef, Candidate: p.Candidate, OldTarget: p.Expected.Target, NewTarget: current.Target, Error: "observed refs changed"}); err != nil {
			return ApplyResult{}, err
		}
		return ApplyResult{}, fmt.Errorf("capsule reconcile: stale plan")
	}
	if _, err := git(ctx, p.Workspace, "fetch", instancePath, resolved); err != nil {
		return ApplyResult{}, err
	}
	if err := r.VCS.UpdateRef(ctx, p.Workspace, p.TargetRef, resolved, current.Target); err != nil {
		return ApplyResult{}, err
	}
	result := ApplyResult{PlanDigest: p.Digest, OldTarget: current.Target, NewTarget: resolved, Applied: true}
	if err := r.emitApplied(ctx, p, result); err != nil {
		return ApplyResult{}, err
	}
	return result, nil
}

func (r Reconciler) AbortContinuation(ctx context.Context, req AbortContinuationRequest) (AbortResult, error) {
	p := req.Plan
	if err := validateConflictPlan(p); err != nil {
		return AbortResult{}, err
	}
	root, err := filepath.Abs(req.ProjectRoot)
	if err != nil {
		return AbortResult{}, err
	}
	instance, err := readIntegrationInstance(root, p.Continuation.Token)
	if err != nil {
		return AbortResult{}, err
	}
	if instance.PlanDigest != p.Digest || instance.ContinuationToken != p.Continuation.Token {
		return AbortResult{}, fmt.Errorf("capsule reconcile: integration instance does not match plan")
	}
	instancePath, err := resolveSyncPath(root, instance.InstancePath)
	if err != nil {
		return AbortResult{}, err
	}
	syncDir := filepath.Join(root, ".capsules", "sync")
	result := AbortResult{PlanDigest: p.Digest, ContinuationToken: p.Continuation.Token, Aborted: true}
	if req.Preserve {
		patch, err := git(ctx, instancePath, "diff", "--binary")
		if err != nil {
			return AbortResult{}, err
		}
		staged, err := git(ctx, instancePath, "diff", "--binary", "--cached")
		if err != nil {
			return AbortResult{}, err
		}
		status, err := git(ctx, instancePath, "status", "--porcelain")
		if err != nil {
			return AbortResult{}, err
		}
		patchPath := filepath.Join(syncDir, p.Continuation.Token+".abort.patch")
		var b strings.Builder
		b.WriteString("# capsule-sync-abort/v1\n")
		b.WriteString("plan_digest: " + p.Digest + "\n")
		b.WriteString("continuation_token: " + p.Continuation.Token + "\n")
		b.WriteString("status:\n")
		for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
			if line != "" {
				b.WriteString("#   " + line + "\n")
			}
		}
		if staged != "" {
			b.WriteString("\n# staged diff\n")
			b.WriteString(staged)
			if !strings.HasSuffix(staged, "\n") {
				b.WriteString("\n")
			}
		}
		if patch != "" {
			b.WriteString("\n# worktree diff\n")
			b.WriteString(patch)
			if !strings.HasSuffix(patch, "\n") {
				b.WriteString("\n")
			}
		}
		if err := os.WriteFile(patchPath, []byte(b.String()), 0o600); err != nil {
			return AbortResult{}, err
		}
		rel, err := filepath.Rel(root, patchPath)
		if err != nil {
			return AbortResult{}, err
		}
		result.PreservedPatch = filepath.ToSlash(rel)
	}
	if err := os.RemoveAll(instancePath); err != nil {
		return AbortResult{}, err
	}
	if err := os.Remove(filepath.Join(syncDir, p.Continuation.Token+".integration.json")); err != nil && !os.IsNotExist(err) {
		return AbortResult{}, err
	}
	if err := r.emit(ctx, Event{Kind: capsuletrace.KindSyncAborted, PlanDigest: p.Digest, Operation: p.Operation, Class: p.Class, TargetRef: p.TargetRef, Candidate: p.Candidate, ContinuationToken: p.Continuation.Token}); err != nil {
		return AbortResult{}, err
	}
	return result, nil
}

func changedPaths(ctx context.Context, dir, from, to string) ([]string, error) {
	return changedPathsWithFilter(ctx, dir, "", from, to)
}

func changedPathsWithFilter(ctx context.Context, dir, filter, from, to string) ([]string, error) {
	args := []string{"diff", "--name-only"}
	if filter != "" {
		args = append(args, filter)
	}
	args = append(args, from, to)
	out, err := git(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	return splitPathLines(out), nil
}

func splitPathLines(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, filepath.ToSlash(line))
		}
	}
	sort.Strings(paths)
	return paths
}

func unmergedPaths(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, dir, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	return splitPathLines(out), nil
}

func validateConflictPlan(p Plan) error {
	if p.Digest == "" || p.Digest != planDigest(p) {
		return fmt.Errorf("capsule reconcile: invalid plan digest")
	}
	if p.Class != Diverged || p.Continuation == nil {
		return fmt.Errorf("capsule reconcile: conflict artifact requires a diverged continuation plan")
	}
	return nil
}

func readIntegrationInstance(root, token string) (IntegrationInstance, error) {
	path := filepath.Join(root, ".capsules", "sync", token+".integration.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return IntegrationInstance{}, err
	}
	var instance IntegrationInstance
	if err := json.Unmarshal(raw, &instance); err != nil {
		return IntegrationInstance{}, err
	}
	if instance.Schema != IntegrationInstanceSchema {
		return IntegrationInstance{}, fmt.Errorf("capsule reconcile: invalid integration instance schema %q", instance.Schema)
	}
	return instance, nil
}

func resolveSyncPath(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("capsule reconcile: integration instance path must be project-relative")
	}
	candidate := filepath.Clean(filepath.Join(root, filepath.FromSlash(rel)))
	syncRoot := filepath.Join(root, ".capsules", "sync")
	if related, err := filepath.Rel(syncRoot, candidate); err != nil || filepath.IsAbs(related) || strings.HasPrefix(related, ".."+string(filepath.Separator)) || related == ".." {
		return "", fmt.Errorf("capsule reconcile: integration instance path escapes sync scope")
	}
	return candidate, nil
}

func gitClone(ctx context.Context, from, to string) error {
	cmd := exec.CommandContext(ctx, "git", "clone", "--no-hardlinks", from, to)
	_, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone integration instance: %w", err)
	}
	return nil
}

func overlap(a, b []string) []string {
	seen := map[string]bool{}
	for _, path := range a {
		seen[path] = true
	}
	var out []string
	for _, path := range b {
		if seen[path] {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}
