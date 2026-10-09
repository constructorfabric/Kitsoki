package ci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/storydigest"
)

const DoctorSchema = "capsule-ci-doctor/v1"

var ErrDoctorNotReady = errors.New("capsule ci: doctor checks failed")

type DoctorCheck struct {
	ID       string         `json:"id"`
	Outcome  string         `json:"outcome"`
	Summary  string         `json:"summary"`
	Details  map[string]any `json:"details,omitempty"`
	Remedies []string       `json:"remedies,omitempty"`
}

type DoctorReport struct {
	Schema         string        `json:"schema"`
	Project        string        `json:"project"`
	Pipeline       string        `json:"pipeline"`
	Workspace      string        `json:"workspace"`
	Ready          bool          `json:"ready"`
	CheckedAt      time.Time     `json:"checked_at"`
	EnvelopeDigest string        `json:"envelope_digest,omitempty"`
	PreparedID     string        `json:"prepared_id,omitempty"`
	Checks         []DoctorCheck `json:"checks"`
}

type DoctorRequest struct {
	Pipeline      string
	Workspace     control.Instance
	WorkspacePath string
}

type WorkspaceInspection struct {
	Path   string `json:"path"`
	Head   string `json:"head"`
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty"`
}

type WorkspaceProbe interface {
	Inspect(context.Context, string) (WorkspaceInspection, error)
}

type WorkspaceProbeFunc func(context.Context, string) (WorkspaceInspection, error)

func (f WorkspaceProbeFunc) Inspect(ctx context.Context, path string) (WorkspaceInspection, error) {
	return f(ctx, path)
}

// GitWorkspaceProbe checks the live checkout rather than trusting instance
// metadata that may have become stale after an interrupted or manual operation.
type GitWorkspaceProbe struct{}

func (GitWorkspaceProbe) Inspect(ctx context.Context, path string) (WorkspaceInspection, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return WorkspaceInspection{}, err
	}
	head, err := gitOutput(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return WorkspaceInspection{}, fmt.Errorf("capsule ci doctor: workspace HEAD: %w", err)
	}
	branch, err := gitOutput(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		branch = ""
	}
	status, err := gitOutput(ctx, path, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return WorkspaceInspection{}, fmt.Errorf("capsule ci doctor: workspace status: %w", err)
	}
	return WorkspaceInspection{Path: path, Head: head, Branch: branch, Dirty: status != ""}, nil
}

func gitOutput(ctx context.Context, path string, args ...string) (string, error) {
	argv := append([]string{"-C", path}, args...)
	out, err := exec.CommandContext(ctx, "git", argv...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message != "" {
			return "", fmt.Errorf("%s: %w", boundedDoctorText(message), err)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

type Doctor struct {
	ProjectRoot string
	Env         environment.Resolver
	Executors   ExecutorSelector
	Hygiene     HygienePlanner
	Workspace   WorkspaceProbe
	LookupEnv   func(string) (string, bool)
	Now         func() time.Time
}

// Check performs a no-spend preflight. It may contact the selected executor's
// capability/prepare endpoints, but it never invokes Provider.Run or a story
// launcher. Expected readiness failures are returned as typed checks rather
// than an opaque error so CLI and MCP callers receive the same remedies.
func (d Doctor) Check(ctx context.Context, req DoctorRequest) (DoctorReport, error) {
	root, err := filepath.Abs(d.ProjectRoot)
	if err != nil {
		return DoctorReport{}, err
	}
	report := DoctorReport{Schema: DoctorSchema, Project: root, Pipeline: req.Pipeline, Workspace: req.Workspace.ID, Ready: true, CheckedAt: d.now(), Checks: []DoctorCheck{}}
	add := func(check DoctorCheck) {
		report.Checks = append(report.Checks, check)
		if check.Outcome == "failed" {
			report.Ready = false
		}
	}

	cfg, err := Load(root)
	if err != nil {
		add(failedDoctorCheck("project-config", err, "fix .kitsoki/ci.yaml and referenced environment/story files"))
		return report, nil
	}
	add(DoctorCheck{ID: "project-config", Outcome: "passed", Summary: "Capsule CI project configuration is valid", Details: map[string]any{"schema": cfg.Schema}})
	pipeline, ok := cfg.Pipelines[req.Pipeline]
	if !ok {
		add(DoctorCheck{ID: "pipeline", Outcome: "failed", Summary: fmt.Sprintf("pipeline %q is not configured", req.Pipeline), Remedies: []string{"add the pipeline under .kitsoki/ci.yaml pipelines"}})
		return report, nil
	}
	pipeline.Cleanup = mergeCleanupPolicy(cfg.Cleanup, pipeline.Cleanup)
	add(DoctorCheck{ID: "pipeline", Outcome: "passed", Summary: "Pipeline is configured", Details: map[string]any{"story": pipeline.Story, "executor": firstNonEmpty(pipeline.Executor, "host")}})

	story, storyErr := storydigest.Compute(root, pipeline.Story)
	if storyErr != nil {
		add(failedDoctorCheck("story-closure", storyErr, "load the story locally and repair missing imports or runtime dependencies"))
	} else {
		add(DoctorCheck{ID: "story-closure", Outcome: "passed", Summary: "Story runtime closure loads and hashes deterministically", Details: map[string]any{"digest": story.Digest, "files": len(story.Files)}})
	}

	inspection, workspaceOK := d.checkWorkspace(ctx, req, add)
	d.checkCredentials(root, cfg, pipeline, add)
	d.checkExecutorTimeouts(cfg, pipeline, add)
	d.checkHygiene(ctx, pipeline.Cleanup, add)
	if storyErr != nil || !workspaceOK {
		return report, nil
	}

	d.Env.ProjectRoot = root
	service := Service{ProjectRoot: root, Env: d.Env}
	_, envelope, planErr := service.Plan(ctx, RunRequest{
		Pipeline:         req.Pipeline,
		Workspace:        control.Handle{ID: req.Workspace.ID, Generation: req.Workspace.Generation},
		DefinitionDigest: req.Workspace.DefinitionDigest,
		SourceDigest:     inspection.Head,
		StoryDigest:      story.Digest,
		Trigger:          Trigger{Kind: "local", RequestedPipeline: req.Pipeline},
	})
	if planErr != nil {
		add(failedDoctorCheck("environment-lock", planErr, "resolve the declared environment locally and repair toolchain, lockfile, or image drift"))
		return report, nil
	}
	report.EnvelopeDigest = envelope.Digest
	add(DoctorCheck{ID: "environment-lock", Outcome: "passed", Summary: "Environment resolves to a sealed execution envelope", Details: map[string]any{"environment": envelope.Environment.ID, "environment_digest": envelope.Environment.Digest, "envelope_digest": envelope.Digest}})

	if d.Executors == nil {
		add(DoctorCheck{ID: "executor-select", Outcome: "failed", Summary: "No executor selector is configured", Remedies: []string{"configure the project Capsule CI executor catalog"}})
		return report, nil
	}
	provider, err := d.Executors.Select(ctx, pipeline.Executor)
	if err != nil {
		add(failedDoctorCheck("executor-select", err, "configure the named executor in .kitsoki/ci.yaml"))
		return report, nil
	}
	if provider == nil {
		add(DoctorCheck{ID: "executor-select", Outcome: "failed", Summary: "Executor selection returned no provider", Remedies: []string{"repair the selected executor adapter"}})
		return report, nil
	}
	add(DoctorCheck{ID: "executor-select", Outcome: "passed", Summary: "Executor is selectable", Details: map[string]any{"executor": firstNonEmpty(pipeline.Executor, "host")}})

	capabilities, err := provider.Describe(ctx)
	if err != nil {
		add(failedDoctorCheck("executor-describe", err, "verify endpoint reachability, TLS, credentials, and worker logs"))
		return report, nil
	}
	add(DoctorCheck{ID: "executor-describe", Outcome: "passed", Summary: "Executor capabilities are reachable", Details: map[string]any{"id": capabilities.ID, "placements": capabilities.Placements, "isolation": capabilities.Isolation, "networks": capabilities.Networks, "environment_refs": capabilities.EnvironmentRefs, "cancellable": capabilities.Cancellable}})
	if err := validateDoctorCapabilities(capabilities, envelope.Policy); err != nil {
		add(failedDoctorCheck("executor-capabilities", err, "select an executor whose network and isolation capabilities satisfy the pipeline"))
		return report, nil
	}
	add(DoctorCheck{ID: "executor-capabilities", Outcome: "passed", Summary: "Executor satisfies the sealed network and sandbox policy"})
	if !d.checkWorkerEnvironmentRefs(root, cfg, pipeline, capabilities, add) {
		return report, nil
	}

	prepared, err := provider.Prepare(ctx, envelope)
	if err != nil {
		add(failedDoctorCheck("executor-prepare", err, "inspect executor policy/materialization diagnostics; doctor did not start a story run"))
		return report, nil
	}
	report.PreparedID = prepared.ID
	add(DoctorCheck{ID: "executor-prepare", Outcome: "passed", Summary: "Executor accepted the sealed envelope without running the story", Details: map[string]any{"prepared_id": prepared.ID, "placement": prepared.Placement}})
	return report, nil
}

func (d Doctor) checkExecutorTimeouts(cfg Config, pipeline Pipeline, add func(DoctorCheck)) {
	if remote, ok := cfg.Remotes[pipeline.Executor]; ok {
		timeouts := executor.DefaultHTTPTimeouts()
		add(DoctorCheck{ID: "executor-timeouts", Outcome: "passed", Summary: "Remote executor requests have bounded deadlines", Details: map[string]any{"endpoint": remote.Endpoint, "connect": timeouts.Connect.String(), "response_header": timeouts.ResponseHeader.String(), "overall": timeouts.Overall.String()}})
		return
	}
	add(DoctorCheck{ID: "executor-timeouts", Outcome: "passed", Summary: "Local executor does not depend on an unbounded HTTP request"})
}

func (d Doctor) checkWorkspace(ctx context.Context, req DoctorRequest, add func(DoctorCheck)) (WorkspaceInspection, bool) {
	if req.Workspace.ID == "" || req.Workspace.Generation == 0 || strings.TrimSpace(req.WorkspacePath) == "" {
		add(DoctorCheck{ID: "workspace", Outcome: "failed", Summary: "A live managed workspace and generation are required", Remedies: []string{"create or inspect the workspace through kitsoki capsule workspace"}})
		return WorkspaceInspection{}, false
	}
	probe := d.Workspace
	if probe == nil {
		probe = GitWorkspaceProbe{}
	}
	inspection, err := probe.Inspect(ctx, req.WorkspacePath)
	if err != nil {
		add(failedDoctorCheck("workspace", err, "repair or recreate the managed workspace, then rerun doctor"))
		return WorkspaceInspection{}, false
	}
	details := map[string]any{"path": inspection.Path, "head": inspection.Head, "branch": inspection.Branch, "dirty": inspection.Dirty, "recorded_head": req.Workspace.Head, "generation": req.Workspace.Generation, "state": req.Workspace.State}
	switch req.Workspace.State {
	case control.StateReady, control.StateCommitted, control.StateIntegrated:
	default:
		add(DoctorCheck{ID: "workspace", Outcome: "failed", Summary: "Managed workspace is not in a CI-runnable lifecycle state", Details: details, Remedies: []string{"finish, recover, or recreate the workspace before CI"}})
		return inspection, false
	}
	if inspection.Dirty {
		add(DoctorCheck{ID: "workspace", Outcome: "failed", Summary: "Workspace contains uncommitted or untracked changes", Details: details, Remedies: []string{"commit or remove workspace changes before CI so the source digest is complete"}})
		return inspection, false
	}
	if req.Workspace.Head == "" || inspection.Head != req.Workspace.Head {
		add(DoctorCheck{ID: "workspace", Outcome: "failed", Summary: "Live workspace HEAD does not match the managed instance source HEAD", Details: details, Remedies: []string{"refresh workspace status/source metadata or recreate the workspace from the intended ref"}})
		return inspection, false
	}
	add(DoctorCheck{ID: "workspace", Outcome: "passed", Summary: "Managed workspace is clean and source HEAD is current", Details: details})
	return inspection, true
}

func (d Doctor) checkCredentials(root string, cfg Config, pipeline Pipeline, add func(DoctorCheck)) {
	lookup := d.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	envID := pipeline.Environment
	if envID == "" {
		envID = cfg.DefaultEnvironment
	}
	var names []string
	remote, isRemote := cfg.Remotes[pipeline.Executor]
	if !isRemote {
		if definition, err := environment.Load(root, envID); err == nil {
			names = append(names, definition.SecretRefs...)
		}
	}
	if isRemote && remote.CredentialEnv != "" {
		names = append(names, remote.CredentialEnv)
	}
	names = uniqueSorted(names)
	missing := make([]string, 0)
	for _, name := range names {
		value, ok := lookup(name)
		if !ok || strings.TrimSpace(value) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		add(DoctorCheck{ID: "credentials", Outcome: "failed", Summary: "Required credential environment variables are missing", Details: map[string]any{"required": names, "missing": missing}, Remedies: []string{"set the named environment variables in the controller process; values are never recorded"}})
		return
	}
	add(DoctorCheck{ID: "credentials", Outcome: "passed", Summary: "Required credential environment variables are present", Details: map[string]any{"required": names}})
}

func (d Doctor) checkWorkerEnvironmentRefs(root string, cfg Config, pipeline Pipeline, capabilities executor.Capabilities, add func(DoctorCheck)) bool {
	if _, remote := cfg.Remotes[pipeline.Executor]; !remote {
		add(DoctorCheck{ID: "worker-environment", Outcome: "passed", Summary: "Local executor uses controller environment grants"})
		return true
	}
	envID := pipeline.Environment
	if envID == "" {
		envID = cfg.DefaultEnvironment
	}
	definition, err := environment.Load(root, envID)
	if err != nil {
		add(failedDoctorCheck("worker-environment", err, "repair the selected environment definition"))
		return false
	}
	required := uniqueSorted(definition.SecretRefs)
	available := uniqueSorted(capabilities.EnvironmentRefs)
	availableSet := map[string]bool{}
	for _, name := range available {
		availableSet[name] = true
	}
	var missing []string
	for _, name := range required {
		if !availableSet[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		add(DoctorCheck{ID: "worker-environment", Outcome: "failed", Summary: "Remote worker does not advertise required story credential grants", Details: map[string]any{"required": required, "available": available, "missing": missing}, Remedies: []string{"restart the Capsule worker with each required, present secret name in --pass-env; values are never advertised"}})
		return false
	}
	add(DoctorCheck{ID: "worker-environment", Outcome: "passed", Summary: "Remote worker advertises every required story credential grant", Details: map[string]any{"required": required, "available": available}})
	return true
}

func (d Doctor) checkHygiene(ctx context.Context, policy CleanupPolicy, add func(DoctorCheck)) {
	if d.Hygiene == nil {
		add(DoctorCheck{ID: "disk-capacity", Outcome: "failed", Summary: "Disk capacity probe is not configured", Remedies: []string{"configure the Capsule hygiene planner for doctor"}})
		add(DoctorCheck{ID: "hygiene-debt", Outcome: "failed", Summary: "Workspace/run hygiene probe is not configured", Remedies: []string{"configure the Capsule hygiene planner for doctor"}})
		return
	}
	report, err := d.Hygiene.PlanHygiene(ctx, policy)
	if err != nil {
		add(failedDoctorCheck("disk-capacity", err, "run kitsoki capsule cleanup plan and repair inventory or filesystem errors"))
		add(failedDoctorCheck("hygiene-debt", err, "run kitsoki capsule cleanup plan and repair inventory or filesystem errors"))
		return
	}
	diskDetails := map[string]any{"known": report.DiskKnown, "capacity_bytes": report.DiskCapacityBytes, "free_bytes": report.DiskFreeBytes, "minimum_free_bytes": report.DiskMinimumBytes, "below_minimum": report.DiskBelowMinimum}
	if !report.DiskKnown {
		add(DoctorCheck{ID: "disk-capacity", Outcome: "warning", Summary: "Filesystem free space could not be determined", Details: diskDetails, Remedies: []string{"check free space before starting remote or multi-workspace CI"}})
	} else if report.DiskBelowMinimum {
		add(DoctorCheck{ID: "disk-capacity", Outcome: "failed", Summary: "Filesystem free space is below the configured safety floor", Details: diskDetails, Remedies: []string{"run kitsoki capsule cleanup plan, then apply a reviewed cleanup plan"}})
	} else {
		add(DoctorCheck{ID: "disk-capacity", Outcome: "passed", Summary: "Filesystem has sufficient free space", Details: diskDetails})
	}
	hygieneDetails := map[string]any{"candidates": report.Candidates, "reclaimable_bytes": report.TotalBytes, "max_reclaimable_bytes": policy.MaxReclaimableBytes, "evidence_ref": report.EvidenceRef}
	if policy.MaxReclaimableBytes > 0 && report.TotalBytes > policy.MaxReclaimableBytes {
		add(DoctorCheck{ID: "hygiene-debt", Outcome: "failed", Summary: "Reclaimable Capsule data exceeds pipeline policy", Details: hygieneDetails, Remedies: []string{"run kitsoki capsule cleanup plan, review candidates, and apply cleanup before CI"}})
	} else {
		add(DoctorCheck{ID: "hygiene-debt", Outcome: "passed", Summary: "Reclaimable Capsule data is within pipeline policy", Details: hygieneDetails})
	}
}

func validateDoctorCapabilities(capabilities executor.Capabilities, policy executor.Policy) error {
	return executor.ValidateCapabilities(capabilities, policy)
}

func failedDoctorCheck(id string, err error, remedy string) DoctorCheck {
	check := DoctorCheck{ID: id, Outcome: "failed", Summary: boundedDoctorText(err.Error())}
	if remedy != "" {
		check.Remedies = []string{remedy}
	}
	return check
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" {
			seen[value] = true
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func boundedDoctorText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) > 512 {
		return value[:512] + "…"
	}
	return value
}

func (d Doctor) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}
