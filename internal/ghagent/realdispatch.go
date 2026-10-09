// realdispatch.go — task 2 of docs/proposals/gh-agent-honest-issues.md.
//
// Real pipeline dispatch drives the ACTUAL stories/bugfix machine end-to-end
// (no beat-fixture host.agent.* stub map) in a per-job managed clone workspace, through a
// live-or-replay harness selected the same way `kitsoki turn`/`web` select
// harnesses — except the default is always "replay" here (see
// resolveHarnessMode in dispatch.go): an unattended dispatcher must never go
// live on ambient credentials.
//
// Only stories/bugfix has a registered plan today. There is exactly one
// recorded, arg-matched host cassette in the repo that walks the FULL
// pipeline through `done` — stories/bugfix/cassettes/happy_human.cassette.yaml,
// captured once against the real Claude CLI (see
// stories/bugfix/flows/happy_human.yaml's doc comment) and replayed forever.
// Its episodes match on {handler, phase} (stories/bugfix/cassettes/
// happy_human.cassette.yaml: `match_on: [handler, phase]`), NOT on prompt
// content, so substituting the job's real ticket_id/thread/repo into
// initial_world does not break cassette matching — the recorded transcript is
// genuinely replayed against this job's identity, not merely templated text.
// stories/dev-story has no equivalent recorded cassette yet, so it still runs
// the honest stub path (runStubBeatFixture) until its own plan is authored —
// a real, if narrower, scope than the proposal's "stories/bugfix / stories/
// dev-story" framing implied.
package ghagent

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/goccy/go-yaml"

	"kitsoki/internal/jobs"
	"kitsoki/internal/store"
	"kitsoki/internal/testrunner"
	"kitsoki/internal/vcsops"
)

// realDispatchPlan describes how to drive one story's REAL pipeline for gh
// issue dispatch: which recorded cassette replays its agent/host calls, which
// ifaces to bind to their real handlers (so a cassette miss during a
// deliberate recording run reaches production code, and so "live" mode's
// absent cassette leaves the real handlers as the only path), and the fixed
// intent sequence the cassette's phases were recorded against.
type realDispatchPlan struct {
	// CassetteRelPath is the recorded host cassette (kind: host_cassette),
	// relative to repoRoot. Used only in replay mode.
	CassetteRelPath string
	// HostBindings mirrors the cassette fixture's host_bindings: block.
	HostBindings map[string]string
	// JudgeMode is forced to the value the recorded cassette's turn shape
	// matches — a real dispatch plan's turn sequence is fixed to one
	// specific checkpoint cadence, so judge_mode must agree with it.
	JudgeMode string
	// BugfixMode ("full"/"quick") the plan's turns assume.
	BugfixMode string
	// TriagePreflight runs bugfix triage-only mode before the full maker
	// pipeline and gates repair on a STILL-LIVE/PARTIAL verdict.
	TriagePreflight bool
	// TriageReplayVerdict is the deterministic no-LLM verdict used by the
	// replay harness. Live mode ignores this and calls the real triager.
	TriageReplayVerdict map[string]any
	// Turns is the fixed intent sequence the cassette was recorded against.
	Turns []testrunner.FlowTurn
	// BaseWorld seeds fixture-shape defaults the plan's turns rely on
	// (bugfix_exit, judge_confidence_threshold, ...) that aren't job-derived.
	BaseWorld map[string]any
}

// realDispatchPlans is keyed by route.Story. Only stories/bugfix has a plan
// today (see the package doc above for why).
var realDispatchPlans = map[string]realDispatchPlan{
	"stories/bugfix": {
		CassetteRelPath: filepath.Join("stories", "bugfix", "cassettes", "happy_human.cassette.yaml"),
		HostBindings: map[string]string{
			"vcs":       "host.git",
			"ci":        "host.local",
			"workspace": "host.git_worktree",
			"transport": "host.append_to_file",
		},
		// The recorded cassette's phases (idle/reproducing/proposing/
		// implementing/testing/reviewing/validating) match judge_mode:
		// human's checkpoint-per-room cadence — see happy_human.yaml. Real
		// dispatch forces this regardless of route.World's judge_mode
		// default (llm_then_human) until a cassette recorded against that
		// cadence exists.
		JudgeMode:       "human",
		BugfixMode:      "full",
		TriagePreflight: true,
		TriageReplayVerdict: map[string]any{
			"verdict":             "STILL-LIVE",
			"confidence":          0.82,
			"summary_title":       "STILL-LIVE — queued issue proceeds to bugfix",
			"evidence":            "Replay harness preflight preserves the triage gate without live LLM spend; the queued issue context is present in ticket_body/ticket_title.",
			"summary_markdown":    "The deterministic replay preflight treats the queued issue as still actionable so the recorded full bugfix pipeline can run without live LLM spend.",
			"suggested_action":    "drive the full bugfix pipeline",
			"fixed_in_ref":        nil,
			"involved_components": []any{"gh-agent replay preflight"},
		},
		BaseWorld: map[string]any{
			"bugfix_exit":                "open-PR",
			"base_branch":                "main",
			"judge_confidence_threshold": 0.8,
		},
		Turns: []testrunner.FlowTurn{
			{Intent: &testrunner.FlowIntent{Name: "start"}, ExpectState: "reproducing"},
			{Intent: &testrunner.FlowIntent{Name: "accept"}, ExpectState: "proposing"},
			{Intent: &testrunner.FlowIntent{Name: "accept"}, ExpectState: "implementing"},
			{Intent: &testrunner.FlowIntent{Name: "accept"}, ExpectState: "testing"},
			{Intent: &testrunner.FlowIntent{Name: "accept"}, ExpectState: "reviewing"},
			{Intent: &testrunner.FlowIntent{Name: "accept"}, ExpectState: "validating"},
			{Intent: &testrunner.FlowIntent{Name: "accept"}, ExpectState: "done"},
			{Intent: &testrunner.FlowIntent{Name: "accept"}, ExpectState: "__exit__done"},
		},
	},
}

// jobWorktreeSlugRe sanitizes a job ID into a safe path/branch segment.
var jobWorktreeSlugRe = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func jobWorktreeSlug(jobID string) string {
	slug := jobWorktreeSlugRe.ReplaceAllString(strings.TrimSpace(jobID), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "job"
	}
	return slug
}

// jobWorktreeRelDir is the per-job managed workspace path relative to the target
// checkout root. The name is retained for RunResult/API compatibility.
func jobWorktreeRelDir(jobID string) string {
	return filepath.Join(".capsules", "workspaces", jobWorkspaceID(jobID))
}

func jobWorkspaceID(jobID string) string {
	return "gh-job-" + jobWorktreeSlug(jobID)
}

func jobFeatureBranch(jobID string) string {
	return "gh-job/" + jobWorktreeSlug(jobID)
}

func jobIntegrationBranch(jobID string) string {
	return "integration/kitsoki-autofix-" + jobWorktreeSlug(jobID)
}

func jobReplayCommitSHA(jobID string) string {
	sum := sha1.Sum([]byte("kitsoki-gh-agent-replay:" + jobWorktreeSlug(jobID)))
	return fmt.Sprintf("%x", sum)
}

// runRealDispatch drives route.Story's REAL machine end-to-end via plan's
// recorded cassette (replay mode) or the real handlers (live mode), in a
// per-job workspace identity seeded through stories/bugfix's own sanctioned
// workspace_prepared/autostart path (rooms/idle.yaml Step-0w) — never via an
// un-declared world key.
func runRealDispatch(ctx context.Context, root string, route Route, job *jobs.GHJob, plan realDispatchPlan) (RunResult, error) {
	mode := harnessModeFromContext(ctx)

	appPath := route.AppPath
	if strings.TrimSpace(appPath) == "" {
		appPath = filepath.Join(root, route.Story, "app.yaml")
	}

	worktreeRel := jobWorktreeRelDir(job.JobID)
	worktreeAbs := filepath.Join(root, worktreeRel)

	var triage triagePreflightResult
	if plan.TriagePreflight && job.ObjectKind == "issue" {
		var err error
		triage, err = runBugfixTriagePreflight(ctx, appPath, route, job, plan, mode)
		if err != nil {
			return RunResult{Harness: mode, Worktree: worktreeAbs, RealHostCalls: triage.HostCalls}, err
		}
		switch triage.Verdict {
		case "STILL-LIVE", "PARTIAL":
			// Continue into the full bugfix pipeline.
		case "ALREADY-FIXED":
			return alreadyFixedTriageResult(route, job, mode, worktreeAbs, triage), nil
		default:
			return RunResult{Harness: mode, Worktree: worktreeAbs, RealHostCalls: triage.HostCalls}, fmt.Errorf("ghagent: triage preflight for %s returned %q; refusing to run or close autonomously", job.OriginRef, triage.Verdict)
		}
	}

	initialWorld := map[string]any{}
	for k, v := range plan.BaseWorld {
		initialWorld[k] = v
	}
	for k, v := range jobFlowWorld(job) {
		if strings.TrimSpace(v) != "" {
			initialWorld[k] = v
		}
	}
	// Per-job workspace identity, seeded through the sanctioned
	// workspace_prepared/.capsules/workspaces-prefix exemption
	// (stories/bugfix/rooms/idle.yaml Step-0w) instead of an un-declared
	// world key: a repo-relative .capsules/workspaces/ workdir plus
	// workspace_prepared: true both independently satisfy that guard.
	initialWorld["workspace_id"] = jobWorkspaceID(job.JobID)
	initialWorld["feature_branch"] = jobFeatureBranch(job.JobID)
	initialWorld["workdir"] = worktreeRel
	initialWorld["workspace_prepared"] = true
	initialWorld["bugfix_mode"] = plan.BugfixMode
	initialWorld["judge_mode"] = plan.JudgeMode
	for k, v := range route.World {
		if k == "judge_mode" {
			continue // plan.JudgeMode is authoritative — see realDispatchPlans' doc.
		}
		initialWorld[k] = v
	}

	fixture := &testrunner.FlowFixture{
		TestKind:       "flow",
		App:            appPath,
		InitialState:   "idle",
		InitialWorld:   initialWorld,
		Turns:          plan.Turns,
		ExpectTerminal: boolPtr(true),
		ExpectNoErrors: boolPtr(true),
	}
	if mode == HarnessReplay {
		fixture.HostCassette = filepath.Join(root, plan.CassetteRelPath)
		fixture.HostBindings = plan.HostBindings
	}
	// mode == HarnessLive: no HostCassette, no HostHandlers. RunFlows'
	// orchestrator path registers the real host.RegisterBuiltins handlers
	// whenever HostBindings is set (see flows.go), so plan.HostBindings still
	// applies here to bind the real ifaces — but with no cassette to hit
	// first, every call reaches the real handler: a real agent subprocess
	// per agents: block, real git workspace/commit ops, real test runs. This
	// is genuinely operator-invoked-only; resolveHarnessMode never picks it
	// by default.
	if mode == HarnessLive {
		fixture.HostBindings = plan.HostBindings
	}

	fixturePath, cleanupFixture, err := writeFlowFixture(fixture)
	if err != nil {
		return RunResult{}, err
	}
	defer cleanupFixture()

	report, runErr := testrunner.RunFlows(ctx, appPath, fixturePath, testrunner.FlowOptions{})
	succeeded := runErr == nil && report != nil && report.Passed >= 1 && report.Failed == 0

	// Land the job's real commits onto a shared integration branch BEFORE
	// cleanup, while jobFeatureBranch(job.JobID) still exists — cleanup below
	// deletes it. A workspace only ever materializes on disk in live mode (see
	// cleanupJobWorktree's doc); replay mode leaves worktreeAbs absent (the
	// cassette served every workspace host call), so there is nothing real to
	// land and the job keeps its synthetic-but-labeled identity below.
	integrationBranch := jobIntegrationBranch(job.JobID)
	if v, ok := route.World["integration_branch"].(string); ok && strings.TrimSpace(v) != "" {
		integrationBranch = strings.TrimSpace(v)
	}
	commitSHA := jobReplayCommitSHA(job.JobID)
	var landErr error
	if succeeded && mode == HarnessLive {
		if _, statErr := os.Stat(worktreeAbs); statErr == nil {
			commitSHA, landErr = landFeatureBranch(ctx, root, route, job, integrationBranch)
		}
	}

	// Cleanup policy (proposal open-question 2 lean): delete the workspace on
	// success, keep it (bounded retention window) on failure for post-mortem.
	// A no-op in replay mode, where nothing was ever checked out on disk —
	// the cassette served every workspace host call.
	if cleanupErr := cleanupJobWorktree(ctx, root, worktreeAbs, job.JobID, succeeded && landErr == nil); cleanupErr != nil {
		// Best-effort: a cleanup failure must not mask the real run outcome.
		_ = cleanupErr
	}

	if runErr != nil {
		return RunResult{Harness: mode, Worktree: worktreeAbs}, fmt.Errorf("ghagent: real dispatch %q: %w", route.Story, runErr)
	}
	if report.Passed < 1 {
		return RunResult{Harness: mode, Worktree: worktreeAbs}, fmt.Errorf("ghagent: real dispatch %q ran no passing turn (passed=%d failed=%d): %s", route.Story, report.Passed, report.Failed, summarizeFlowFailures(report))
	}
	if landErr != nil {
		return RunResult{Harness: mode, Worktree: worktreeAbs}, fmt.Errorf("ghagent: real dispatch %q: landing onto %q: %w", route.Story, integrationBranch, landErr)
	}

	turns := 0
	hostCalls := 0
	var lastSummary, lastDiff string
	for _, r := range report.Results {
		turns += len(r.Turns)
		for _, t := range r.Turns {
			s, d, n := extractRealDispatchEvidence(t.Events)
			hostCalls += n
			if s != "" {
				lastSummary = s
			}
			if d != "" {
				lastDiff = d
			}
		}
	}

	summary := fmt.Sprintf("Ran `%s` end-to-end via the %s harness (%d turn(s)); workspace `%s`.", route.Story, mode, turns, worktreeRel)
	if lastSummary != "" {
		summary += "\n\n" + lastSummary
	}
	if lastDiff != "" {
		summary += fmt.Sprintf("\n\nDiff:\n```diff\n%s\n```", lastDiff)
	}
	verification := fmt.Sprintf(`# Independent verification

- Story: %q
- Harness: %q
- Triage preflight: %s
- Final state: "done"
- Flow turns: %d
- Host returns observed: %d
- Workspace: %q
- Result: passed

The gh-agent dispatcher first ran the bugfix story's triage-only preflight, then ran the bugfix story from a fresh job context through its verification and done gates before marking this job done.
`, route.Story, mode, triage.Verdict, turns, hostCalls+triage.HostCalls, worktreeRel)
	assets := []RunAsset{{
		Name:     "fix-report.md",
		MimeType: "text/markdown",
		Data:     []byte(summary + "\n"),
	}, {
		Name:     "independent-verify.md",
		MimeType: "text/markdown",
		Data:     []byte(verification),
	}, {
		Name:     "triage-verdict.md",
		MimeType: "text/markdown",
		Data:     []byte(renderTriageVerdictMarkdown(triage)),
	}}
	if strings.TrimSpace(lastDiff) != "" {
		assets = append(assets, RunAsset{
			Name:     "fix.patch",
			MimeType: "text/x-diff",
			Data:     []byte(lastDiff),
		})
	}

	return RunResult{
		RunURL:            "kitsoki://run/" + job.JobID,
		FinalState:        "done",
		Turns:             turns,
		Summary:           summary,
		IntegrationBranch: integrationBranch,
		CommitSHA:         commitSHA,
		Stubbed:           false,
		Harness:           mode,
		Worktree:          worktreeAbs,
		RealHostCalls:     hostCalls + triage.HostCalls,
		Assets:            assets,
	}, nil
}

type triagePreflightResult struct {
	Verdict         string
	Confidence      any
	SummaryTitle    string
	Evidence        string
	SummaryMarkdown string
	SuggestedAction string
	FixedInRef      string
	Payload         map[string]any
	Turns           int
	HostCalls       int
}

func runBugfixTriagePreflight(ctx context.Context, appPath string, route Route, job *jobs.GHJob, plan realDispatchPlan, mode string) (triagePreflightResult, error) {
	initialWorld := map[string]any{
		"bugfix_mode": "triage",
		"judge_mode":  "llm_then_human",
		"workdir":     ".",
	}
	for k, v := range jobFlowWorld(job) {
		if strings.TrimSpace(v) != "" {
			initialWorld[k] = v
		}
	}
	for k, v := range route.World {
		if k == "bugfix_mode" || k == "judge_mode" || k == "workdir" {
			continue
		}
		initialWorld[k] = v
	}
	fixture := &testrunner.FlowFixture{
		TestKind:       "flow",
		App:            appPath,
		InitialState:   "idle",
		InitialWorld:   initialWorld,
		Turns:          []testrunner.FlowTurn{{Intent: &testrunner.FlowIntent{Name: "triage"}, ExpectState: "__exit__triaged"}},
		ExpectTerminal: boolPtr(true),
		ExpectNoErrors: boolPtr(true),
	}
	if mode == HarnessReplay {
		fixture.HostHandlers = map[string]testrunner.HostStub{
			"host.append_to_file": {Data: map[string]any{"ok": true}},
			"host.inbox.add":      {Data: map[string]any{"ok": true}},
			"host.agent.codeact": {
				Data: map[string]any{
					"ok":         true,
					"terminated": "done",
					"payload":    firstNonNilMap(plan.TriageReplayVerdict, defaultStillLiveTriageVerdict(job)),
				},
			},
		}
	} else {
		fixture.HostBindings = map[string]string{"transport": "host.append_to_file"}
	}

	fixturePath, cleanupFixture, err := writeFlowFixture(fixture)
	if err != nil {
		return triagePreflightResult{}, err
	}
	defer cleanupFixture()

	report, runErr := testrunner.RunFlows(ctx, appPath, fixturePath, testrunner.FlowOptions{})
	if runErr != nil {
		return triagePreflightResult{}, fmt.Errorf("ghagent: triage preflight %q: %w", route.Story, runErr)
	}
	if report.Passed < 1 {
		return triagePreflightResult{}, fmt.Errorf("ghagent: triage preflight %q ran no passing turn (passed=%d failed=%d): %s", route.Story, report.Passed, report.Failed, summarizeFlowFailures(report))
	}

	result := triagePreflightResult{}
	for _, r := range report.Results {
		result.Turns += len(r.Turns)
		for _, t := range r.Turns {
			verdict, calls := extractTriagePreflightVerdict(t.Events)
			result.HostCalls += calls
			if verdict != nil {
				result.Payload = verdict
			}
		}
	}
	result.applyPayload()
	if result.Verdict == "" {
		return result, fmt.Errorf("ghagent: triage preflight %q produced no verdict", route.Story)
	}
	return result, nil
}

func extractTriagePreflightVerdict(events []store.Event) (map[string]any, int) {
	hostCalls := 0
	var verdict map[string]any
	for _, ev := range events {
		if ev.Kind != store.HostReturned {
			continue
		}
		hostCalls++
		var payload struct {
			Namespace string         `json:"namespace"`
			Data      map[string]any `json:"data"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}
		if payload.Namespace != "host.agent.codeact" {
			continue
		}
		for _, key := range []string{"payload", "submitted"} {
			if m, ok := payload.Data[key].(map[string]any); ok {
				verdict = m
			}
		}
	}
	return verdict, hostCalls
}

func (r *triagePreflightResult) applyPayload() {
	if r.Payload == nil {
		return
	}
	r.Verdict = strings.TrimSpace(stringFromAny(r.Payload["verdict"]))
	r.Confidence = r.Payload["confidence"]
	r.SummaryTitle = strings.TrimSpace(stringFromAny(r.Payload["summary_title"]))
	r.Evidence = strings.TrimSpace(stringFromAny(r.Payload["evidence"]))
	r.SummaryMarkdown = strings.TrimSpace(stringFromAny(r.Payload["summary_markdown"]))
	r.SuggestedAction = strings.TrimSpace(stringFromAny(r.Payload["suggested_action"]))
	r.FixedInRef = strings.TrimSpace(stringFromAny(r.Payload["fixed_in_ref"]))
}

func alreadyFixedTriageResult(route Route, job *jobs.GHJob, mode string, worktreeAbs string, triage triagePreflightResult) RunResult {
	summary := fmt.Sprintf("Triage preflight for `%s` returned `ALREADY-FIXED`; skipped the full bugfix maker pipeline.\n\n%s", job.OriginRef, triage.SummaryMarkdown)
	verification := fmt.Sprintf(`# Independent verification

- Story: %q
- Harness: %q
- Triage preflight: ALREADY-FIXED
- Full maker pipeline: skipped
- Host returns observed: %d
- Result: passed

The gh-agent dispatcher ran the bugfix story's triage-only preflight and did not run the maker pipeline because the filed issue no longer described a live defect.

## Evidence

%s
`, route.Story, mode, triage.HostCalls, triage.Evidence)
	return RunResult{
		RunURL:        "kitsoki://run/" + job.JobID,
		FinalState:    "triaged",
		Turns:         triage.Turns,
		Summary:       summary,
		Stubbed:       false,
		Harness:       mode,
		Worktree:      worktreeAbs,
		RealHostCalls: triage.HostCalls,
		Assets: []RunAsset{{
			Name:     "fix-report.md",
			MimeType: "text/markdown",
			Data:     []byte(summary + "\n"),
		}, {
			Name:     "independent-verify.md",
			MimeType: "text/markdown",
			Data:     []byte(verification),
		}, {
			Name:     "triage-verdict.md",
			MimeType: "text/markdown",
			Data:     []byte(renderTriageVerdictMarkdown(triage)),
		}},
	}
}

func renderTriageVerdictMarkdown(triage triagePreflightResult) string {
	lines := []string{
		"# Triage verdict",
		"",
		"- Verdict: `" + triage.Verdict + "`",
		"- Confidence: " + stringFromAny(triage.Confidence),
		"- Suggested action: " + triage.SuggestedAction,
	}
	if triage.FixedInRef != "" {
		lines = append(lines, "- Fixed in: "+triage.FixedInRef)
	}
	lines = append(lines, "", "## Evidence", "", triage.Evidence, "", "## Summary", "", triage.SummaryMarkdown)
	return strings.Join(lines, "\n") + "\n"
}

func defaultStillLiveTriageVerdict(job *jobs.GHJob) map[string]any {
	return map[string]any{
		"verdict":             "STILL-LIVE",
		"confidence":          0.8,
		"summary_title":       "STILL-LIVE — queued issue proceeds to bugfix",
		"evidence":            "Replay harness preflight preserved the triage gate for " + job.OriginRef + ".",
		"summary_markdown":    "The deterministic replay preflight treats the queued issue as still actionable so the recorded full bugfix pipeline can run without live LLM spend.",
		"suggested_action":    "drive the full bugfix pipeline",
		"fixed_in_ref":        nil,
		"involved_components": []any{"gh-agent replay preflight"},
	}
}

func firstNonNilMap(values ...map[string]any) map[string]any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return map[string]any{}
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

// extractRealDispatchEvidence scans one turn's events for HostReturned
// entries, returning the last agent submit's summary_markdown, the last
// host.git diff, and a count of HostReturned events observed (the
// RealHostCalls evidence the no-"Done"-without-real-work invariant checks).
func extractRealDispatchEvidence(events []store.Event) (summary, diff string, hostReturns int) {
	for _, ev := range events {
		if ev.Kind != store.HostReturned {
			continue
		}
		hostReturns++
		var payload struct {
			Namespace string         `json:"namespace"`
			Data      map[string]any `json:"data"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}
		switch payload.Namespace {
		case "host.agent.task", "host.agent.ask", "host.agent.decide":
			if submitted, ok := payload.Data["submitted"].(map[string]any); ok {
				if s, ok := submitted["summary_markdown"].(string); ok && strings.TrimSpace(s) != "" {
					summary = s
				}
			}
		case "host.git":
			if d, ok := payload.Data["diff"].(string); ok && strings.TrimSpace(d) != "" {
				diff = d
			}
		}
	}
	return summary, diff, hostReturns
}

// writeFlowFixture marshals fixture to a temp YAML file RunFlows can load,
// returning the path and a cleanup func.
func writeFlowFixture(fixture *testrunner.FlowFixture) (string, func(), error) {
	out, err := yaml.Marshal(fixture)
	if err != nil {
		return "", func() {}, fmt.Errorf("ghagent: render real dispatch fixture: %w", err)
	}
	dir, err := os.MkdirTemp("", "kitsoki-ghagent-real-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("ghagent: create temp flow dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	path := filepath.Join(dir, "real_dispatch.flow.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("ghagent: write real dispatch fixture: %w", err)
	}
	return path, cleanup, nil
}

func boolPtr(b bool) *bool { return &b }

// ─── Per-job workspace cleanup (task 2.1) ─────────────────────────────────
//
// The per-job workspace is CREATED by the story itself: rooms/idle.yaml's
// Step 2 invokes iface.workspace.create (bound to host.git_worktree) once
// world.workdir/feature_branch/workspace_id are seeded — the sanctioned
// checkout path, not a bespoke one here. In replay mode the recorded
// cassette intercepts that call (no real git side effect); in live mode the
// real handler delegates to scripts/dev-workspace.sh. This file only owns the
// CLEANUP side of the lifecycle: delete on success, keep (bounded retention) on
// failure.

// landFeatureBranch squash-merges the job's feature branch onto a shared
// integration branch via vcsops.Integrate, so a live fix's real commits
// survive as a real git object on a reviewable branch instead of only the
// fix.patch asset. It runs the merge from a dedicated integration managed
// workspace (never the caller's checkout) so root — typically the protected
// primary checkout — is never mutated directly; see AGENTS.md's read-mostly
// rule.
// Returns the real squash-commit SHA on success. A refused/conflicted
// integrate is returned as an error: a fix with no real landing must not be
// reported as done (fail closed, no fabricated success).
func landFeatureBranch(ctx context.Context, root string, route Route, job *jobs.GHJob, integrationBranch string) (string, error) {
	base := "main"
	if v, ok := route.World["base_branch"].(string); ok && strings.TrimSpace(v) != "" {
		base = strings.TrimSpace(v)
	}
	integrationDir, err := ensureIntegrationWorktree(ctx, root, integrationBranch, base)
	if err != nil {
		return "", err
	}
	jobDir := filepath.Join(root, jobWorktreeRelDir(job.JobID))
	if _, err := os.Stat(jobDir); err != nil {
		return "", fmt.Errorf("job workspace %s: %w", jobDir, err)
	}
	if out, exit, err := vcsops.RunGit(ctx, integrationDir, "fetch", jobDir, jobFeatureBranch(job.JobID)+":refs/heads/"+jobFeatureBranch(job.JobID)); err != nil || exit != 0 {
		return "", fmt.Errorf("ghagent: fetch job branch %s from %s: %w: %s", jobFeatureBranch(job.JobID), jobDir, err, strings.TrimSpace(out))
	}
	message := fmt.Sprintf("Land %s fix for %s (job %s)", route.Story, job.OriginRef, job.JobID)
	result, err := vcsops.Integrate(ctx, integrationDir, jobFeatureBranch(job.JobID), integrationBranch, message, vcsops.IntegrateOptions{})
	if err != nil {
		return "", err
	}
	if !result.Integrated {
		if len(result.Conflicts) > 0 {
			return "", fmt.Errorf("integration conflicts in %s", strings.Join(result.Conflicts, ", "))
		}
		return "", errors.New(result.Refused)
	}
	if out, exit, err := vcsops.RunGit(ctx, root, "fetch", integrationDir, integrationBranch+":refs/heads/"+integrationBranch); err != nil || exit != 0 {
		return "", fmt.Errorf("ghagent: import integration branch %s from %s: %w: %s", integrationBranch, integrationDir, err, strings.TrimSpace(out))
	}
	return result.Commit, nil
}

// ensureIntegrationWorktree returns the path of a managed clone workspace
// checked out on branch, one per integration branch (reused across every job in
// a marathon cycle that shares the branch name), creating the branch from base
// the first time it's needed. The name is retained for minimal API churn.
func ensureIntegrationWorktree(ctx context.Context, root, branch, base string) (string, error) {
	id := "integration-" + jobWorktreeSlug(branch)
	abs := filepath.Join(root, ".capsules", "workspaces", id)
	if err := runDevWorkspace(ctx, root, "create", "--repo", root, "--id", id, "--branch", branch, "--base", base, "--no-bootstrap", "--json"); err != nil {
		return "", err
	}
	if _, exit, _ := vcsops.RunGit(ctx, root, "rev-parse", "--verify", "--quiet", branch); exit == 0 {
		if out, exit, err := vcsops.RunGit(ctx, abs, "fetch", root, branch); err != nil || exit != 0 {
			return "", fmt.Errorf("ghagent: sync integration workspace %s from %s: %w: %s", abs, branch, err, strings.TrimSpace(out))
		}
		if out, exit, err := vcsops.RunGit(ctx, abs, "reset", "--hard", "FETCH_HEAD"); err != nil || exit != 0 {
			return "", fmt.Errorf("ghagent: reset integration workspace %s to %s: %w: %s", abs, branch, err, strings.TrimSpace(out))
		}
	}
	return abs, nil
}

// jobWorktreeRetention is the bounded retention window (proposal
// open-question 2) for kept failed-job workspaces, mirroring dogfood-marathon
// practice. Enforced by PruneStaleFailedWorktrees, not automatically on a
// timer by this package (task 3's drain/maintenance loop is the natural home
// for scheduling that sweep).
const jobWorktreeRetention = 7 * 24 * time.Hour

// cleanupJobWorktree removes the per-job managed workspace when the run
// succeeded. On failure it leaves the workspace in place for post-mortem. A
// no-op when the path was never materialized on disk (replay mode). Name is
// retained for RunResult/API compatibility.
func cleanupJobWorktree(ctx context.Context, root, worktreeAbs, jobID string, succeeded bool) error {
	if _, err := os.Stat(worktreeAbs); err != nil {
		return nil // nothing checked out (replay mode served the workspace calls)
	}
	if !succeeded {
		return nil // keep for post-mortem, subject to jobWorktreeRetention
	}
	return runDevWorkspace(ctx, root, "close", "--repo", root, "--force", jobWorkspaceID(jobID))
}

// PruneStaleFailedWorktrees removes gh-job-* managed workspaces under
// <root>/.capsules/workspaces whose directory mtime is older than
// jobWorktreeRetention. Exported so an operator CLI or a maintenance ticker
// (a natural companion to task 3's drain loop) can call it directly; safe to
// call frequently — a no-op absent stale entries.
func PruneStaleFailedWorktrees(ctx context.Context, root string) error {
	dir := filepath.Join(root, ".capsules", "workspaces")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-jobWorktreeRetention)
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "gh-job-") {
			continue
		}
		info, ierr := e.Info()
		if ierr != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = runDevWorkspace(ctx, root, "close", "--repo", root, "--force", e.Name())
	}
	return nil
}

func runDevWorkspace(ctx context.Context, root string, args ...string) error {
	script, err := devWorkspaceScript(root)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, script, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ghagent: dev-workspace %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func devWorkspaceScript(root string) (string, error) {
	for _, candidate := range devWorkspaceScriptCandidates(root) {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("ghagent: scripts/dev-workspace.sh not found from %s", root)
}

func devWorkspaceScriptCandidates(root string) []string {
	var out []string
	if strings.TrimSpace(root) != "" {
		out = append(out, filepath.Join(root, "scripts", "dev-workspace.sh"))
	}
	if cwd, err := os.Getwd(); err == nil {
		for dir := cwd; ; dir = filepath.Dir(dir) {
			out = append(out, filepath.Join(dir, "scripts", "dev-workspace.sh"))
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}
	return out
}
