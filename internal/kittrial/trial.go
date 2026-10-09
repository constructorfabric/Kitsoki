// Package kittrial is the `kitsoki kit trial` engine — the S7 acceptance
// judge for a staged kit update (docs/proposals/kit-lifecycle.md).
//
// A trial runs four gates against the STAGED resolution of one kit, without
// mutating anything an accepted run depends on:
//
//	contract       kit verify (contract checks + the kit's own conformance
//	               flows + extends re-runs) against the staged tree, plus a
//	               staged-resolved load of every consumer instance. Hard.
//	frozen_replay  every consumer flow fixture replayed twice — leg A
//	               against the LOCKED tree (a sanity leg: its failure means
//	               the fixture rotted locally and is never blamed on the
//	               upgrade), leg B against the staged tree. The fixture's
//	               own assertions decide; the route diff explains. Hard.
//	onboarding     the idempotent validation sweep: project checks against
//	               staged resolution, profile schema validation, and the
//	               no-side-effects invariant (I1: the gate run leaves
//	               .kitsoki/.mcp.json/.agents/.claude byte-identical,
//	               modulo the trial's own workdir and the ledger). Hard.
//	baseline_live  the validation-ledger consult: already-validated cases
//	               SKIP (zero spend); no-cost oracles validate now by
//	               replay; live oracles queue for operator approval.
//	               Advisory.
//
// Blocking is worklist-driven: every hard-gate failure lands as an
// error-severity worklist item, and the trial result is blocked exactly
// when open error items exist — which is what makes operator waivers
// (status: accepted, audited via the receipt's worklist digest) meaningful.
package kittrial

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/kitlock"
	"kitsoki/internal/kitstage"
	"kitsoki/internal/kitverify"
	"kitsoki/internal/kitworklist"
	"kitsoki/internal/projectprofile"
	"kitsoki/internal/taskcase"
	"kitsoki/internal/testrunner"
)

// Gate ids.
const (
	GateContract     = "contract"
	GateFrozenReplay = "frozen_replay"
	GateOnboarding   = "onboarding"
	GateBaselineLive = "baseline_live"
)

// Gate statuses.
const (
	StatusPass    = "pass"
	StatusFail    = "fail"
	StatusPartial = "partial"
	StatusSkipped = "skipped"
)

// Check is one named deterministic check inside a gate.
type Check struct {
	ID     string `json:"id"`
	Status string `json:"status"` // ok | failed | skipped | missing
	Detail string `json:"detail,omitempty"`
}

// Issue is a gate finding destined for the migration worklist.
type Issue struct {
	Kind            string
	Severity        string
	Subject         string
	Detail          string
	SuggestedAction string
	Evidence        string
}

// FixtureResult is one consumer fixture's dual-leg replay outcome.
type FixtureResult struct {
	Fixture     string     `json:"fixture"`
	Instance    string     `json:"instance"`
	BaselineLeg string     `json:"baseline_leg"` // pass | fail | unavailable
	StagedLeg   string     `json:"staged_leg"`   // pass | fail
	Failures    []string   `json:"failures,omitempty"`
	Drift       *RouteDiff `json:"drift,omitempty"`
}

// CaseResult is one baseline task case's ledger-gated outcome.
type CaseResult struct {
	CaseID    string  `json:"case_id"`
	Status    string  `json:"status"` // skipped_already_validated | validated_replay | failed | pending_approval
	LedgerRef string  `json:"ledger_ref,omitempty"`
	CostUSD   float64 `json:"cost_usd"`
	Detail    string  `json:"detail,omitempty"`
}

// GateResult is one gate's full outcome.
type GateResult struct {
	ID       string          `json:"id"`
	Status   string          `json:"status"`
	Spend    SpendAudit      `json:"spend"`
	Checks   []Check         `json:"checks,omitempty"`
	Fixtures []FixtureResult `json:"fixtures,omitempty"`
	Cases    []CaseResult    `json:"cases,omitempty"`
	Issues   []Issue         `json:"-"`
}

// Report is the full trial outcome.
type Report struct {
	Kit          string            `json:"kit"`
	From         kitstage.Snapshot `json:"from"`
	To           kitstage.Snapshot `json:"to"`
	Result       string            `json:"result"`
	Gates        []GateResult      `json:"gates"`
	Spend        SpendAudit        `json:"spend"`
	LedgerRefs   []string          `json:"ledger_refs,omitempty"`
	Worklist     *kitworklist.File `json:"-"`
	WorklistPath string            `json:"worklist_path"`
	ReceiptPath  string            `json:"receipt_path"`
}

// Options configures a trial run. The two cmd-layer seams are injected:
// BaseResolver (cmd/kitsoki's buildImportResolver) and ProjectChecks (the
// project-tools upgrade sweep, which lives beside the CLI).
type Options struct {
	ProjectRoot string
	KitName     string
	// BaseResolver resolves everything that is not the kit under trial.
	BaseResolver app.ImportResolver
	// Resolvers are the candidate resolvers live-verified tree resolution
	// tries, in order, for BOTH legs (see VerifiedTreeDir). Defaults to
	// [BaseResolver]. The CLI also passes an overrides-skipping variant so
	// the LOCKED leg can find the old bytes when a kit-dev override points
	// the name at the new ones.
	Resolvers []app.ImportResolver
	// Flow passes through to every fixture replay (verbosity etc.); its
	// ImportResolver is overridden per leg.
	Flow testrunner.FlowOptions
	// Extends resolves extends: bases for the contract gate (nil skips
	// base-kit re-runs, reported per entry).
	Extends kitverify.ExtendsResolver
	// ProjectChecks runs the project-tools upgrade sweep with the given
	// resolver (staged). nil marks the check skipped.
	ProjectChecks func(ctx context.Context, resolver app.ImportResolver) ([]Check, error)
	// ArtifactsRoot defaults to <ProjectRoot>/.artifacts/kit-trial.
	ArtifactsRoot string
	// Progress receives one line per gate when non-nil.
	Progress io.Writer
	// Now is injectable for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// Run executes a full trial of the staged candidate for opts.KitName.
func Run(ctx context.Context, opts Options) (*Report, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ArtifactsRoot == "" {
		opts.ArtifactsRoot = filepath.Join(opts.ProjectRoot, ".artifacts", "kit-trial")
	}

	staged, locked, err := loadStagedPair(opts.ProjectRoot, opts.KitName)
	if err != nil {
		return nil, err
	}
	liveResolvers := opts.Resolvers
	if len(liveResolvers) == 0 && opts.BaseResolver != nil {
		liveResolvers = []app.ImportResolver{opts.BaseResolver}
	}
	// Both legs prefer a LIVE, hash-verified directory over the tree-cache
	// snapshot: identical content guarantees, but relative sibling imports
	// (dev-story's ../bugfix layout) still resolve. Git-tier pins skip this
	// — the commit cache holds the whole repository.
	stagedDir := ""
	if staged.Commit == "" {
		if dir, ok := kitstage.VerifiedTreeDir(staged.Source, opts.ProjectRoot, staged.TreeHash, liveResolvers); ok {
			stagedDir = dir
		}
	}
	if stagedDir == "" {
		stagedDir, err = kitstage.ResolveTree(staged)
		if err != nil {
			return nil, err
		}
	}
	acceptedDir, acceptedOK := "", false
	if locked != nil && locked.Commit == "" {
		acceptedDir, acceptedOK = kitstage.VerifiedTreeDir(locked.Source, opts.ProjectRoot, locked.TreeHash, liveResolvers)
	}
	if !acceptedOK {
		acceptedDir, acceptedOK = kitstage.AcceptedTree(locked)
	}
	instances := kitstage.InstanceApps(opts.ProjectRoot)

	t := &trialRun{
		opts:        opts,
		staged:      staged,
		locked:      locked,
		stagedDir:   stagedDir,
		acceptedDir: acceptedDir,
		acceptedOK:  acceptedOK,
		instances:   instances,
		stagedResolver: kitstage.PinnedResolver(opts.BaseResolver,
			map[string]string{opts.KitName: stagedDir}),
	}
	if acceptedOK {
		t.lockedResolver = kitstage.PinnedResolver(opts.BaseResolver,
			map[string]string{opts.KitName: acceptedDir})
	}

	// I1 pre-image: the trial itself may write ONLY the trial workdir and
	// the validation ledger; everything else under the project-tool
	// surfaces must be byte-identical afterwards.
	pre, preErr := t.sideEffectDigest()

	gates := []GateResult{
		t.gateContract(ctx),
		t.gateFrozenReplay(ctx),
		t.gateOnboarding(ctx),
		t.gateBaselineLive(ctx),
	}

	if preErr == nil {
		post, postErr := t.sideEffectDigest()
		check := Check{ID: "no-side-effects", Status: "ok", Detail: "project surfaces byte-identical across the trial"}
		if postErr != nil {
			check = Check{ID: "no-side-effects", Status: "failed", Detail: postErr.Error()}
		} else if pre != post {
			check = Check{ID: "no-side-effects", Status: "failed", Detail: "trial mutated project files outside its workdir/ledger"}
			gates[2].Issues = append(gates[2].Issues, Issue{
				Kind: "onboarding", Severity: kitworklist.SeverityError,
				Subject: "no-side-effects",
				Detail:  "the trial run mutated .kitsoki/.mcp.json/.agents/.claude outside .kitsoki/kit-update and the validation ledger",
			})
			gates[2].Status = StatusFail
		}
		gates[2].Checks = append(gates[2].Checks, check)
	}

	report := &Report{Kit: opts.KitName, From: staged.From, To: staged.Snapshot(), Gates: gates}
	for i := range gates {
		report.Spend.Add(gates[i].Spend)
	}
	for _, g := range gates {
		for _, c := range g.Cases {
			if c.LedgerRef != "" {
				report.LedgerRefs = append(report.LedgerRefs, c.LedgerRef)
			}
		}
	}

	if err := t.writeWorklist(report); err != nil {
		return nil, err
	}
	report.Result = deriveResult(report)
	if err := t.writeReceipt(report); err != nil {
		return nil, err
	}
	return report, nil
}

type trialRun struct {
	opts           Options
	staged         *kitstage.Entry
	locked         *kitlock.Entry
	stagedDir      string
	acceptedDir    string
	acceptedOK     bool
	instances      []string
	stagedResolver app.ImportResolver
	lockedResolver app.ImportResolver
}

func loadStagedPair(projectRoot, name string) (*kitstage.Entry, *kitlock.Entry, error) {
	sf, err := kitstage.Load(kitstage.Path(projectRoot))
	if err != nil {
		return nil, nil, err
	}
	staged := sf.Kits[name]
	if staged == nil {
		return nil, nil, fmt.Errorf("kit %q has no staged candidate under %s — run `kitsoki kit update %s` first", name, projectRoot, name)
	}
	lf, err := kitlock.Load(kitlock.Path(projectRoot))
	if err != nil {
		return nil, nil, err
	}
	return staged, lf.Kits[name], nil
}

func (t *trialRun) progress(format string, args ...any) {
	if t.opts.Progress != nil {
		fmt.Fprintf(t.opts.Progress, format+"\n", args...)
	}
}

// ─── Gate 1: contract ────────────────────────────────────────────────────────

func (t *trialRun) gateContract(ctx context.Context) GateResult {
	g := GateResult{ID: GateContract, Status: StatusPass}

	if _, err := os.Stat(filepath.Join(t.stagedDir, "kit.yaml")); err == nil {
		rep, err := kitverify.VerifyKit(t.stagedDir, kitverify.Options{
			ImportResolver: t.stagedResolver,
			Flow:           t.flowOptions(t.stagedResolver),
			Extends:        t.opts.Extends,
		})
		switch {
		case err != nil:
			g.Status = StatusFail
			g.Checks = append(g.Checks, Check{ID: "kit-verify", Status: "failed", Detail: err.Error()})
			g.Issues = append(g.Issues, Issue{Kind: "contract", Severity: kitworklist.SeverityError,
				Subject: "kit-verify", Detail: err.Error()})
		case !rep.OK():
			g.Status = StatusFail
			g.Checks = append(g.Checks, Check{ID: "kit-verify", Status: "failed", Detail: "contract checks or conformance flows failed"})
			for _, s := range rep.Stories {
				for _, issue := range s.Issues {
					g.Issues = append(g.Issues, Issue{Kind: "contract", Severity: kitworklist.SeverityError,
						Subject: s.Story, Detail: issue})
				}
			}
			for _, issue := range rep.ParamIssues {
				g.Issues = append(g.Issues, Issue{Kind: "contract", Severity: kitworklist.SeverityError,
					Subject: "parameters", Detail: issue})
			}
			for _, f := range rep.Flows {
				if f.Err != nil {
					g.Issues = append(g.Issues, Issue{Kind: "flow", Severity: kitworklist.SeverityError,
						Subject: f.Pattern, Detail: f.Err.Error(), Evidence: f.AppPath})
					continue
				}
				if f.Report == nil {
					continue
				}
				g.Spend.Add(AuditFlowResults(f.Report.Results))
				for _, fr := range f.Report.Results {
					if fr.Passed || fr.Skipped {
						continue
					}
					g.Issues = append(g.Issues, Issue{Kind: "flow", Severity: kitworklist.SeverityError,
						Subject: fr.File, Detail: flowFailureSummary(fr), Evidence: f.AppPath})
				}
			}
		default:
			for _, f := range rep.Flows {
				if f.Report != nil {
					g.Spend.Add(AuditFlowResults(f.Report.Results))
				}
			}
			g.Checks = append(g.Checks, Check{ID: "kit-verify", Status: "ok",
				Detail: fmt.Sprintf("%d stories, %d flow suites", len(rep.Stories), len(rep.Flows))})
		}
	} else {
		g.Checks = append(g.Checks, Check{ID: "kit-verify", Status: "skipped", Detail: "no kit.yaml — story-only kit"})
	}

	for _, inst := range t.instances {
		rel := relTo(t.opts.ProjectRoot, inst)
		if _, err := app.LoadWithResolver(inst, nil, t.stagedResolver); err != nil {
			g.Status = StatusFail
			g.Checks = append(g.Checks, Check{ID: "instance-load:" + rel, Status: "failed", Detail: err.Error()})
			g.Issues = append(g.Issues, Issue{Kind: "load", Severity: kitworklist.SeverityError,
				Subject: rel, Detail: fmt.Sprintf("does not load against the staged %s: %v", t.opts.KitName, err)})
		} else {
			g.Checks = append(g.Checks, Check{ID: "instance-load:" + rel, Status: "ok"})
		}
	}
	t.progress("gate %s: %s", g.ID, g.Status)
	return g
}

// ─── Gate 2: frozen replay ───────────────────────────────────────────────────

func (t *trialRun) gateFrozenReplay(ctx context.Context) GateResult {
	g := GateResult{ID: GateFrozenReplay, Status: StatusPass}

	type source struct{ instance, fixture string }
	var sources []source
	for _, inst := range t.instances {
		matches, _ := filepath.Glob(filepath.Join(filepath.Dir(inst), "flows", "*.yaml"))
		sort.Strings(matches)
		for _, m := range matches {
			if strings.HasSuffix(m, ".cassette.yaml") {
				continue
			}
			sources = append(sources, source{instance: inst, fixture: m})
		}
		baselines, _ := filepath.Glob(filepath.Join(t.opts.ProjectRoot, ".kitsoki", "qa", "baselines", "*", "flow.yaml"))
		sort.Strings(baselines)
		for _, b := range baselines {
			sources = append(sources, source{instance: inst, fixture: b})
		}
	}
	if len(sources) == 0 {
		g.Status = StatusSkipped
		g.Checks = append(g.Checks, Check{ID: "fixtures", Status: "skipped", Detail: "no consumer flow fixtures found"})
		t.progress("gate %s: %s", g.ID, g.Status)
		return g
	}

	for _, src := range sources {
		fr := FixtureResult{
			Fixture:  relTo(t.opts.ProjectRoot, src.fixture),
			Instance: relTo(t.opts.ProjectRoot, src.instance),
		}

		// Leg A — sanity replay against the LOCKED tree. A failure here
		// means the fixture no longer matches the accepted version (local
		// drift since freeze); it is excluded from the upgrade verdict.
		fr.BaselineLeg = "unavailable"
		var baselineResult *testrunner.FlowResult
		if t.lockedResolver != nil {
			repA, errA := testrunner.RunFlows(ctx, src.instance, src.fixture, t.flowOptions(t.lockedResolver))
			switch {
			case errA != nil:
				fr.BaselineLeg = "fail"
				g.Issues = append(g.Issues, Issue{Kind: "stale_baseline_leg", Severity: kitworklist.SeverityWarn,
					Subject: fr.Fixture, Detail: fmt.Sprintf("fixture fails against the LOCKED version (excluded from upgrade verdict): %v", errA)})
			case len(repA.Results) > 0 && repA.Results[0].Passed:
				fr.BaselineLeg = "pass"
				baselineResult = &repA.Results[0]
				g.Spend.Add(AuditFlowResults(repA.Results))
			default:
				fr.BaselineLeg = "fail"
				detail := "fixture fails against the LOCKED version (excluded from upgrade verdict)"
				if len(repA.Results) > 0 {
					detail += ": " + flowFailureSummary(repA.Results[0])
					g.Spend.Add(AuditFlowResults(repA.Results))
				}
				g.Issues = append(g.Issues, Issue{Kind: "stale_baseline_leg", Severity: kitworklist.SeverityWarn,
					Subject: fr.Fixture, Detail: detail})
			}
		}

		// Leg B — the verdict leg, against the staged tree.
		repB, errB := testrunner.RunFlows(ctx, src.instance, src.fixture, t.flowOptions(t.stagedResolver))
		switch {
		case errB != nil:
			fr.StagedLeg = "fail"
			fr.Failures = []string{errB.Error()}
		case len(repB.Results) == 0:
			fr.StagedLeg = "fail"
			fr.Failures = []string{"no fixture results returned"}
		default:
			res := repB.Results[0]
			g.Spend.Add(AuditFlowResults(repB.Results))
			if res.Passed || res.Skipped {
				fr.StagedLeg = "pass"
			} else {
				fr.StagedLeg = "fail"
				fr.Failures = collectFailures(res)
			}
			if baselineResult != nil {
				d := DiffRuns(baselineResult, &res)
				fr.Drift = &d
			}
		}

		// A staged-leg failure only counts against the upgrade when the
		// fixture is healthy at the locked version (or no locked leg was
		// available to prove otherwise — fail closed in that case too,
		// since the fixture's own assertions are the contract).
		if fr.StagedLeg == "fail" && fr.BaselineLeg != "fail" {
			g.Status = StatusFail
			kind := "flow"
			detail := strings.Join(fr.Failures, "; ")
			if strings.Contains(detail, "cassette") {
				kind = "cassette_miss"
			}
			g.Issues = append(g.Issues, Issue{Kind: kind, Severity: kitworklist.SeverityError,
				Subject: fr.Fixture, Detail: detail, Evidence: fr.Instance})
		}
		g.Fixtures = append(g.Fixtures, fr)
	}
	t.progress("gate %s: %s (%d fixtures)", g.ID, g.Status, len(g.Fixtures))
	return g
}

// ─── Gate 3: onboarding / validation ─────────────────────────────────────────

func (t *trialRun) gateOnboarding(ctx context.Context) GateResult {
	g := GateResult{ID: GateOnboarding, Status: StatusPass}

	if t.opts.ProjectChecks != nil {
		checks, err := t.opts.ProjectChecks(ctx, t.stagedResolver)
		if err != nil {
			g.Status = StatusFail
			g.Checks = append(g.Checks, Check{ID: "project-checks", Status: "failed", Detail: err.Error()})
			g.Issues = append(g.Issues, Issue{Kind: "onboarding", Severity: kitworklist.SeverityError,
				Subject: "project-checks", Detail: err.Error()})
		} else {
			for _, c := range checks {
				g.Checks = append(g.Checks, c)
				if c.Status == "error" || c.Status == "failed" {
					g.Status = StatusFail
					g.Issues = append(g.Issues, Issue{Kind: "onboarding", Severity: kitworklist.SeverityError,
						Subject: c.ID, Detail: c.Detail})
				}
			}
		}
	} else {
		g.Checks = append(g.Checks, Check{ID: "project-checks", Status: "skipped", Detail: "no project-checks runner wired"})
	}

	profilePath := filepath.Join(t.opts.ProjectRoot, ".kitsoki", "project-profile.yaml")
	if _, err := os.Stat(profilePath); err == nil {
		res, err := projectprofile.ValidateFile(profilePath, t.opts.ProjectRoot)
		switch {
		case err != nil:
			g.Status = StatusFail
			g.Checks = append(g.Checks, Check{ID: "profile-validate", Status: "failed", Detail: err.Error()})
			g.Issues = append(g.Issues, Issue{Kind: "onboarding", Severity: kitworklist.SeverityError,
				Subject: "project-profile.yaml", Detail: err.Error()})
		case !res.OK:
			g.Status = StatusFail
			detail := strings.Join(append(append([]string{}, res.Schema...), res.Semantic...), "; ")
			g.Checks = append(g.Checks, Check{ID: "profile-validate", Status: "failed", Detail: detail})
			g.Issues = append(g.Issues, Issue{Kind: "onboarding", Severity: kitworklist.SeverityError,
				Subject: "project-profile.yaml", Detail: detail})
		default:
			g.Checks = append(g.Checks, Check{ID: "profile-validate", Status: "ok"})
		}
	} else {
		g.Checks = append(g.Checks, Check{ID: "profile-validate", Status: "skipped", Detail: "no project profile"})
	}
	t.progress("gate %s: %s", g.ID, g.Status)
	return g
}

// ─── Gate 4: baseline live (ledger-gated) ────────────────────────────────────

func (t *trialRun) gateBaselineLive(ctx context.Context) GateResult {
	g := GateResult{ID: GateBaselineLive, Status: StatusPass}

	casesDir := filepath.Join(t.opts.ProjectRoot, ".kitsoki", "qa", "taskcases")
	if _, err := os.Stat(casesDir); err != nil {
		g.Status = StatusSkipped
		g.Checks = append(g.Checks, Check{ID: "taskcases", Status: "skipped", Detail: "no .kitsoki/qa/taskcases directory"})
		t.progress("gate %s: %s", g.ID, g.Status)
		return g
	}
	cases, err := taskcase.LoadAll(casesDir)
	if err != nil {
		g.Status = StatusFail
		g.Checks = append(g.Checks, Check{ID: "taskcases", Status: "failed", Detail: err.Error()})
		return g
	}

	instanceDigest, err := InstanceDigest(t.instances)
	if err != nil {
		g.Status = StatusFail
		g.Checks = append(g.Checks, Check{ID: "instance-digest", Status: "failed", Detail: err.Error()})
		return g
	}
	ledgerPath := LedgerPath(t.opts.ProjectRoot)
	ledger, err := LoadLedger(ledgerPath)
	if err != nil {
		g.Status = StatusFail
		g.Checks = append(g.Checks, Check{ID: "ledger", Status: "failed", Detail: err.Error()})
		return g
	}

	ledgerDirty := false
	for _, c := range cases {
		cr := CaseResult{CaseID: c.ID}
		oracleSHA, err := OracleSHA256(c.Oracle, t.opts.ProjectRoot)
		if err != nil {
			cr.Status = "failed"
			cr.Detail = err.Error()
			g.Cases = append(g.Cases, cr)
			g.Status = StatusPartial
			continue
		}

		if hit := ledger.Lookup(c.ID, t.staged.TreeHash, instanceDigest, oracleSHA); hit != nil && hit.Result == "pass" {
			cr.Status = "skipped_already_validated"
			cr.LedgerRef = fmt.Sprintf("%s@%s", c.ID, shortHash(t.staged.TreeHash))
			g.Cases = append(g.Cases, cr)
			continue
		}

		switch c.Oracle.Kind {
		case taskcase.OracleFlowFixture:
			// No-cost validation: replay the comparator fixture against the
			// staged resolution right now.
			comparator := c.Oracle.Comparator
			if !filepath.IsAbs(comparator) {
				comparator = filepath.Join(t.opts.ProjectRoot, comparator)
			}
			instance := t.instanceFor(c)
			started := t.opts.Now()
			rep, runErr := testrunner.RunFlows(ctx, instance, comparator, t.flowOptions(t.stagedResolver))
			pass := runErr == nil && rep != nil && rep.Failed == 0 && rep.Passed > 0
			if rep != nil {
				g.Spend.Add(AuditFlowResults(rep.Results))
			}
			entry := LedgerEntry{
				CaseID:         c.ID,
				Kit:            t.opts.KitName,
				KitTreeHash:    t.staged.TreeHash,
				InstanceDigest: instanceDigest,
				Oracle:         OracleRef{Kind: string(c.Oracle.Kind), Ref: c.Oracle.Comparator, SHA256: oracleSHA},
				Result:         "fail",
				Mode:           "replay",
				DurationMS:     t.opts.Now().Sub(started).Milliseconds(),
				RecordedAt:     t.opts.Now().UTC().Format(time.RFC3339),
			}
			if pass {
				entry.Result = "pass"
				cr.Status = "validated_replay"
				cr.LedgerRef = fmt.Sprintf("%s@%s", c.ID, shortHash(t.staged.TreeHash))
			} else {
				cr.Status = "failed"
				if runErr != nil {
					cr.Detail = runErr.Error()
				} else if rep != nil {
					for _, r := range rep.Results {
						if !r.Passed && !r.Skipped {
							cr.Detail = flowFailureSummary(r)
							break
						}
					}
				}
				g.Status = StatusPartial
				g.Issues = append(g.Issues, Issue{Kind: "case", Severity: kitworklist.SeverityWarn,
					Subject: c.ID, Detail: "baseline case failed replay validation: " + cr.Detail,
					Evidence: relTo(t.opts.ProjectRoot, c.Path)})
			}
			ledger.Append(entry)
			ledgerDirty = true
			g.Cases = append(g.Cases, cr)

		default:
			// A live drive (red_green, agent_eval, ...) spends — it stays
			// queued behind explicit operator approval (the --live lane).
			cr.Status = "pending_approval"
			cr.Detail = fmt.Sprintf("oracle %s requires a live run (cost policy: %s)", c.Oracle.Kind, c.CostPolicy.LivePolicy)
			g.Cases = append(g.Cases, cr)
			if g.Status == StatusPass {
				g.Status = StatusPartial
			}
		}
	}

	if ledgerDirty {
		if err := SaveLedger(ledgerPath, ledger); err != nil {
			g.Status = StatusFail
			g.Checks = append(g.Checks, Check{ID: "ledger", Status: "failed", Detail: err.Error()})
		}
	}
	t.progress("gate %s: %s (%d cases)", g.ID, g.Status, len(g.Cases))
	return g
}

// instanceFor picks the instance app a case's fixture runs against — the
// case's own story.app when it points at an instance, else the project's
// first instance.
func (t *trialRun) instanceFor(c *taskcase.Case) string {
	if c.Story.App != "" {
		p := c.Story.App
		if !filepath.IsAbs(p) {
			p = filepath.Join(t.opts.ProjectRoot, p)
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if len(t.instances) > 0 {
		return t.instances[0]
	}
	return ""
}

// ─── Worklist / receipt / result ─────────────────────────────────────────────

func (t *trialRun) writeWorklist(report *Report) error {
	var fresh []kitworklist.Item
	for _, g := range report.Gates {
		for _, is := range g.Issues {
			item := kitworklist.NewItem(is.Kind, is.Severity, is.Subject, is.Detail)
			item.SuggestedAction = is.SuggestedAction
			item.Evidence = is.Evidence
			fresh = append(fresh, item)
		}
	}
	// The update plan's applicable rename hints join the worklist so the
	// operator addresses everything in one place.
	if plan, err := kitstage.LoadPlan(t.opts.ProjectRoot, t.opts.KitName); err == nil && plan != nil {
		for _, h := range plan.RenameHints {
			if h.Detected == "" {
				continue
			}
			item := kitworklist.NewItem("rename", kitworklist.SeverityWarn, h.Detected,
				fmt.Sprintf("%s renamed upstream: %s -> %s", h.Category, h.Old, h.New))
			item.SuggestedAction = h.SuggestedAction
			fresh = append(fresh, item)
		}
	}

	path := kitworklist.Path(t.opts.ProjectRoot, t.opts.KitName)
	prev, err := kitworklist.Load(path)
	if err != nil {
		return err
	}
	wl := &kitworklist.File{
		Schema:      kitworklist.Schema,
		Kit:         t.opts.KitName,
		From:        t.staged.From,
		To:          t.staged.Snapshot(),
		GeneratedAt: t.opts.Now().UTC().Format(time.RFC3339),
		Items:       kitworklist.Merge(fresh, prev),
	}
	if err := kitworklist.Save(path, wl); err != nil {
		return err
	}
	report.Worklist = wl
	report.WorklistPath = path
	return nil
}

// deriveResult maps gate + worklist state to the trial verdict. Blocking is
// worklist-driven (open error items), which is what makes operator waivers
// effective. Baseline cases that are pending approval or failed hold the
// result at partial (accept then needs --allow-partial). Open WARN/INFO
// items — a stale fixture after an intentional rename, drift notes — stay
// visible in the worklist but do not gate: they are advisory by design,
// and forcing a waiver flag for them would train operators to reach for
// bypass flags.
func deriveResult(report *Report) string {
	if report.Worklist.OpenErrors() > 0 {
		return ResultBlocked
	}
	for _, g := range report.Gates {
		for _, c := range g.Cases {
			if c.Status == "pending_approval" || c.Status == "failed" {
				return ResultPartial
			}
		}
	}
	return ResultReady
}

func (t *trialRun) writeReceipt(report *Report) error {
	receipt := &Receipt{
		Schema: ReceiptSchema,
		Kit:    t.opts.KitName,
		Event:  EventTrial,
		Result: report.Result,
		From:   report.From,
		To:     report.To,
		Spend:  report.Spend,
		LedgerRefs: append([]string(nil),
			report.LedgerRefs...),
		Worklist:       relTo(t.opts.ProjectRoot, report.WorklistPath),
		WorklistDigest: FileDigest(report.WorklistPath),
		SourceDigests:  SourceDigests(t.opts.ProjectRoot, t.instances, t.staged.TreeHash),
		GeneratedAt:    t.opts.Now().UTC().Format(time.RFC3339),
	}
	for _, g := range report.Gates {
		gs := GateSummary{ID: g.ID, Status: g.Status, Spend: g.Spend, Checks: len(g.Checks), Fixtures: len(g.Fixtures)}
		for _, fr := range g.Fixtures {
			if fr.BaselineLeg == "fail" {
				gs.StaleBaseline++
			}
			if fr.Drift != nil && !fr.Drift.Identical {
				if fr.StagedLeg == "pass" {
					gs.DriftBenign++
				} else {
					gs.DriftViolations++
				}
			}
		}
		for _, c := range g.Cases {
			switch c.Status {
			case "skipped_already_validated":
				gs.SkippedLedger++
			case "validated_replay":
				gs.ValidatedNow++
			case "pending_approval":
				gs.PendingCases++
			case "failed":
				gs.FailedCases++
			}
		}
		receipt.Gates = append(receipt.Gates, gs)
	}
	path := TrialReceiptPath(t.opts.ArtifactsRoot, t.opts.KitName, t.staged.TreeHash)
	if err := WriteReceipt(path, receipt); err != nil {
		return err
	}
	report.ReceiptPath = path
	return nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func (t *trialRun) flowOptions(resolver app.ImportResolver) testrunner.FlowOptions {
	opts := t.opts.Flow
	opts.ImportResolver = resolver
	return opts
}

// sideEffectDigest hashes the project-tool surfaces the trial must not
// mutate: .kitsoki (minus the trial's own kit-update workdir and the
// validation ledger, which are its two sanctioned writes), .mcp.json,
// .agents, .claude.
func (t *trialRun) sideEffectDigest() (string, error) {
	root := t.opts.ProjectRoot
	excludeDir := filepath.Join(root, ".kitsoki", kitstage.UpdateDirName)
	excludeLedger := LedgerPath(root)

	h := sha256.New()
	addFile := func(path string) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		h.Write([]byte(rel))
		h.Write([]byte{0})
		h.Write(b)
		h.Write([]byte{0})
		return nil
	}
	for _, top := range []string{".kitsoki", ".agents", ".claude"} {
		dir := filepath.Join(root, top)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if path == excludeDir {
					return fs.SkipDir
				}
				return nil
			}
			if path == excludeLedger || !d.Type().IsRegular() {
				return nil
			}
			return addFile(path)
		})
		if err != nil {
			return "", fmt.Errorf("kittrial: side-effect sweep %s: %w", top, err)
		}
	}
	if p := filepath.Join(root, ".mcp.json"); fileExists(p) {
		if err := addFile(p); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func relTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

func collectFailures(r testrunner.FlowResult) []string {
	var out []string
	for _, turn := range r.Turns {
		out = append(out, turn.Failures...)
	}
	return out
}

func flowFailureSummary(r testrunner.FlowResult) string {
	fails := collectFailures(r)
	if len(fails) == 0 {
		return "failed with no per-turn failure detail"
	}
	if len(fails) > 3 {
		return strings.Join(fails[:3], "; ") + fmt.Sprintf(" (+%d more)", len(fails)-3)
	}
	return strings.Join(fails, "; ")
}
