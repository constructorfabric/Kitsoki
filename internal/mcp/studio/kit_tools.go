package studio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/kitgit"
	"kitsoki/internal/kitlock"
	"kitsoki/internal/kitstage"
	"kitsoki/internal/kittrial"
	"kitsoki/internal/kitverify"
	"kitsoki/internal/kitworklist"
)

// kit_tools.go — the S7 kit-lifecycle tools the studio server exposes so an
// attached agent can drive update → trial → accept/reject over MCP
// (docs/proposals/kit-lifecycle.md, PR5). Every tool wraps the same engine
// the CLI verbs use:
//
//   - kit.status  — kits.lock + kits.staged.lock + worklist counts (read-only).
//   - kit.update  — kitstage.Update (stage a semver-gated candidate; the
//     accepted lockfile is never touched).
//   - kit.trial   — kittrial.Run (contract / frozen_replay / onboarding /
//     baseline_live gates, replay-strict). A blocked result is DATA, not a
//     tool error — the same posture as host.run's red gate.
//   - kit.accept  — kittrial.Accept (fail-closed on the trial receipt).
//   - kit.reject  — kitstage.Remove (residue-free drop).
//
// kit.status is the only tool a read-only server keeps: everything else
// writes project state (staging, worklists, receipts, the lockfile).
//
// The interpretive-free doctrine holds: no tool here makes an LLM call.
// kit.trial replays cassettes under KITSOKI_CASSETTE_STRICT=1 exactly like
// the CLI, so a cassette miss fails closed instead of recording or spending.

// KitTrialDeps carries the cmd-layer seams the kit lifecycle tools cannot
// build for themselves (they live beside the CLI in package main). It is
// the kit twin of WithImportResolver: cmd/kitsoki/mcp.go supplies the
// production values; tests inject fixtures. Every field is optional — a
// missing seam degrades the specific behaviour that needs it (documented
// per field) rather than the whole tool family.
type KitTrialDeps struct {
	// BaseResolver is the CLI's buildImportResolver: kit-dev overrides,
	// $KITSOKI_REPO, then the embedded library. kittrial.Run pins the kit
	// under trial over it for both legs.
	BaseResolver app.ImportResolver
	// PlainResolver is the CLI's buildPlainImportResolver — BaseResolver
	// minus per-kit overrides — passed as a live-verification candidate so
	// the LOCKED leg can find the accepted bytes even while a kit-dev
	// override points the name at the candidate checkout.
	PlainResolver app.ImportResolver
	// ProjectChecks runs the project-tools upgrade sweep against the given
	// project root with the given (staged) resolver — the closure over
	// cmd/kitsoki's checkProjectUpgrade. Nil marks the trial's
	// project-checks entry skipped (kittrial's documented degradation).
	ProjectChecks func(ctx context.Context, projectRoot string, resolver app.ImportResolver) ([]kittrial.Check, error)
	// Extends builds the extends: resolver for the contract gate — the
	// closure over cmd/kitsoki's lockfileExtendsResolver. Nil skips
	// base-kit conformance re-runs (reported per entry).
	Extends func(ctx context.Context, projectRoot string) kitverify.ExtendsResolver
	// ResolveEntry is cmd/kitsoki's resolveKitEntry — the source-resolution
	// seam kit.update stages candidates through. Nil makes kit.update
	// report KIT_UNAVAILABLE (staging silently without the CLI's resolver
	// stack would pin the wrong bytes).
	ResolveEntry kitstage.ResolveEntryFunc
}

// WithKitTrialDeps threads the cmd-layer kit lifecycle seams into the
// kit.* tools, mirroring how cmd/kitsoki/mcp.go passes WithImportResolver.
func WithKitTrialDeps(deps KitTrialDeps) ServerOption {
	return func(s *Server) { s.kitDeps = deps }
}

// registerKitTools wires the kit.* lifecycle tools onto the server. Called
// from the legacy toolbox registration so they share one registry. Only the
// read tool survives readOnly: update/trial/accept/reject all write project
// state.
func (srv *Server) registerKitTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "kit.status",
		Description: "Kit lifecycle status for a project: accepted kits.lock entries joined with staged update candidates (kits.staged.lock) and migration-worklist counts. {target?} defaults to the project root at or above the bound workspace.",
	}, srv.handleKitStatus)

	if srv.readOnly {
		return
	}

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "kit.update",
		Description: "Stage a kit update candidate (kitsoki kit update): re-resolve the locked source, gate on the recorded semver constraint, and stage the result beside the lock — kits.lock is never touched. {name, target?, to?, source?, check_only?, detailed?}. Returns from/to snapshots plus changed-file and rename-hint counts; detailed=true adds the full file list and hints.",
	}, srv.handleKitUpdate)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "kit.trial",
		Description: "Judge a staged kit update (kitsoki kit trial): contract, frozen dual-leg replay, onboarding, and ledger-gated baseline gates, replay-strict (a cassette miss fails closed; no LLM spend). Writes the migration worklist + trial receipt. {name, target?, detailed?}. Returns {result, gates:[{id,status,cost_usd}], worklist counts, receipt_path}; result:\"blocked\" is data, not a tool error. detailed=true adds fixtures, cases, and open worklist items.",
	}, srv.handleKitTrial)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "kit.accept",
		Description: "Promote a staged kit update into kits.lock (kitsoki kit accept), fail-closed on the trial receipt: a receipt for the exact staged tree, a non-blocked result (partial needs allow_partial), and unchanged source digests are required unless force. {name, target?, force?, allow_partial?}.",
	}, srv.handleKitAccept)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "kit.reject",
		Description: "Drop a staged kit update candidate residue-free (kitsoki kit reject): removes the kits.staged.lock entry and the kit-update workdir; the accepted resolution is untouched. {name, target?}.",
	}, srv.handleKitReject)
}

// ── tool args / results ──────────────────────────────────────────────────────

// KitSnapshot is the wire projection of kitstage.Snapshot (which carries
// only YAML tags — serialized raw it would leak Go field names).
type KitSnapshot struct {
	Version  string `json:"version,omitempty"`
	Commit   string `json:"commit,omitempty"`
	TreeHash string `json:"tree_hash,omitempty"`
}

func kitSnapshot(s kitstage.Snapshot) KitSnapshot {
	return KitSnapshot{Version: s.Version, Commit: s.Commit, TreeHash: s.TreeHash}
}

// KitWorklistCounts is the migration worklist's (open, resolved, accepted)
// summary — the token-diet view; open items ride kit.trial detailed=true.
type KitWorklistCounts struct {
	Open     int `json:"open"`
	Resolved int `json:"resolved"`
	Accepted int `json:"accepted"`
}

// KitStatusArgs is the input to kit.status.
type KitStatusArgs struct {
	// Target overrides the project root (absolute, or relative to the
	// bound workspace dir — relative paths are confined there). Empty
	// resolves the nearest .kitsoki project root at or above the workspace.
	Target string `json:"target,omitempty"`
}

// KitStatusOK is the kit.status result.
type KitStatusOK struct {
	OK     bool            `json:"ok"`     // always true on this branch
	Target string          `json:"target"` // the resolved project root
	Kits   []KitStatusItem `json:"kits"`   // union of locked and staged kits, sorted by name
}

// KitStatusItem is one kit's lifecycle state.
type KitStatusItem struct {
	Name string `json:"name"`
	// Version/TreeHash/Constraint describe the ACCEPTED lock entry (empty
	// for a kit that is staged but not locked).
	Version    string `json:"version,omitempty"`
	TreeHash   string `json:"tree_hash,omitempty"`
	Constraint string `json:"constraint,omitempty"`
	// Staged is the pending update candidate, when one is staged.
	Staged *KitStagedInfo `json:"staged,omitempty"`
	// Worklist summarizes the kit's migration worklist, when one exists.
	Worklist *KitWorklistCounts `json:"worklist,omitempty"`
}

// KitStagedInfo describes a staged candidate.
type KitStagedInfo struct {
	Version  string `json:"version,omitempty"`
	TreeHash string `json:"tree_hash,omitempty"`
	StagedAt string `json:"staged_at,omitempty"`
}

// KitUpdateArgs is the input to kit.update.
type KitUpdateArgs struct {
	// Name is the locked kit to re-resolve.
	Name string `json:"name"`
	// Target overrides the project root (see KitStatusArgs.Target).
	Target string `json:"target,omitempty"`
	// To rewrites a git source's @ref; for other tiers it is a version
	// constraint the candidate must satisfy (bare version = exact).
	To string `json:"to,omitempty"`
	// Source replaces the kit's source string entirely.
	Source string `json:"source,omitempty"`
	// CheckOnly resolves and plans without staging anything.
	CheckOnly bool `json:"check_only,omitempty"`
	// Detailed includes the full changed-file list and rename hints (token
	// diet: counts only by default).
	Detailed bool `json:"detailed,omitempty"`
}

// KitUpdateOK is the kit.update result.
type KitUpdateOK struct {
	OK  bool   `json:"ok"` // always true on this branch (the engine ran)
	Kit string `json:"kit"`
	// Staged reports the candidate + plan were persisted (false under
	// check_only and up_to_date).
	Staged bool `json:"staged"`
	// UpToDate reports the candidate resolved to exactly the accepted
	// content — nothing to stage.
	UpToDate          bool        `json:"up_to_date,omitempty"`
	From              KitSnapshot `json:"from"`
	To                KitSnapshot `json:"to"`
	ChangedFilesCount int         `json:"changed_files_count"`
	// ChangedFilesNote explains a skipped diff (accepted tree not
	// materialized locally) — the plan is a courtesy summary, the trial
	// gates are the real judge.
	ChangedFilesNote string `json:"changed_files_note,omitempty"`
	RenameHintsCount int    `json:"rename_hints_count"`
	PlanPath         string `json:"plan_path,omitempty"`
	// ChangedFiles / RenameHints are populated only when detailed=true.
	ChangedFiles []KitFileChange `json:"changed_files,omitempty"`
	RenameHints  []KitRenameHint `json:"rename_hints,omitempty"`
}

// KitFileChange is one changed file in the upgrade plan.
type KitFileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // added | removed | modified
}

// KitRenameHint is one compat.renamed hint, annotated with the consumer
// reference it touches (Detected empty = declared upstream, not detected
// locally).
type KitRenameHint struct {
	Category        string `json:"category"`
	Old             string `json:"old"`
	New             string `json:"new"`
	Detected        string `json:"detected,omitempty"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// KitTrialArgs is the input to kit.trial.
type KitTrialArgs struct {
	Name string `json:"name"`
	// Target overrides the project root (see KitStatusArgs.Target).
	Target string `json:"target,omitempty"`
	// Detailed includes per-gate fixtures, cases, and the open worklist
	// items (token diet: {id,status,cost_usd} per gate by default).
	Detailed bool `json:"detailed,omitempty"`
}

// KitTrialOK is the kit.trial result. OK means the trial RAN to a verdict;
// the verdict itself is Result — a blocked trial is data the agent acts on
// (address worklist items), not a tool error.
type KitTrialOK struct {
	OK           bool               `json:"ok"`
	Kit          string             `json:"kit"`
	Result       string             `json:"result"` // ready | partial | blocked
	From         KitSnapshot        `json:"from"`
	To           KitSnapshot        `json:"to"`
	Gates        []KitTrialGateItem `json:"gates"`
	Worklist     KitWorklistCounts  `json:"worklist"`
	WorklistPath string             `json:"worklist_path"`
	ReceiptPath  string             `json:"receipt_path"`
	// OpenItems lists the open worklist items (only when detailed=true).
	OpenItems []KitWorklistItem `json:"open_items,omitempty"`
}

// KitTrialGateItem is one gate's outcome. CostUSD is the MEASURED live LLM
// spend (cassette-replayed calls never count — see kittrial.SpendAudit).
type KitTrialGateItem struct {
	ID      string  `json:"id"`
	Status  string  `json:"status"`
	CostUSD float64 `json:"cost_usd"`
	// Fixtures / Cases are populated only when detailed=true.
	Fixtures []KitTrialFixtureItem `json:"fixtures,omitempty"`
	Cases    []KitTrialCaseItem    `json:"cases,omitempty"`
}

// KitTrialFixtureItem is one consumer fixture's dual-leg replay outcome.
type KitTrialFixtureItem struct {
	Fixture     string   `json:"fixture"`
	BaselineLeg string   `json:"baseline_leg,omitempty"` // pass | fail | unavailable
	StagedLeg   string   `json:"staged_leg"`             // pass | fail
	Failures    []string `json:"failures,omitempty"`
}

// KitTrialCaseItem is one baseline task case's ledger-gated outcome.
type KitTrialCaseItem struct {
	CaseID    string `json:"case_id"`
	Status    string `json:"status"`
	LedgerRef string `json:"ledger_ref,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// KitWorklistItem is one open migration-worklist item projected to the wire.
type KitWorklistItem struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	Severity        string `json:"severity"`
	Subject         string `json:"subject"`
	Detail          string `json:"detail"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

// KitAcceptArgs is the input to kit.accept.
type KitAcceptArgs struct {
	Name string `json:"name"`
	// Target overrides the project root (see KitStatusArgs.Target).
	Target string `json:"target,omitempty"`
	// Force skips the trial-receipt gate entirely (recorded in the
	// acceptance receipt).
	Force bool `json:"force,omitempty"`
	// AllowPartial accepts a partial trial (recorded in the receipt).
	AllowPartial bool `json:"allow_partial,omitempty"`
}

// KitAcceptOK is the kit.accept result.
type KitAcceptOK struct {
	OK           bool   `json:"ok"` // always true on this branch (promotion landed)
	Kit          string `json:"kit"`
	Version      string `json:"version,omitempty"`
	TreeHash     string `json:"tree_hash,omitempty"`
	LockPath     string `json:"lock_path"`
	ReceiptPath  string `json:"receipt_path"`
	AcceptedWith string `json:"accepted_with,omitempty"` // "" | partial | force
}

// KitRejectArgs is the input to kit.reject.
type KitRejectArgs struct {
	Name string `json:"name"`
	// Target overrides the project root (see KitStatusArgs.Target).
	Target string `json:"target,omitempty"`
}

// KitRejectOK is the kit.reject result.
type KitRejectOK struct {
	OK      bool   `json:"ok"` // always true on this branch (candidate dropped)
	Kit     string `json:"kit"`
	Version string `json:"version,omitempty"` // the dropped candidate's version
}

// ── handlers ──────────────────────────────────────────────────────────────────

// handleKitStatus joins the accepted lockfile with the staged candidates and
// per-kit worklist counts. Pure reads — available on a read-only server.
func (srv *Server) handleKitStatus(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args KitStatusArgs,
) (*mcpsdk.CallToolResult, any, error) {
	root, rerr := srv.resolveKitTarget(args.Target)
	if rerr != nil {
		return rerr, nil, nil
	}
	lf, err := kitlock.Load(kitlock.Path(root))
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	sf, err := kitstage.Load(kitstage.Path(root))
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}

	names := map[string]bool{}
	for n := range lf.Kits {
		names[n] = true
	}
	for n := range sf.Kits {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)

	out := KitStatusOK{OK: true, Target: root, Kits: make([]KitStatusItem, 0, len(sorted))}
	for _, n := range sorted {
		item := KitStatusItem{Name: n}
		if locked := lf.Kits[n]; locked != nil {
			item.Version = locked.Version
			item.TreeHash = locked.TreeHash
			item.Constraint = locked.Constraint
		}
		if staged := sf.Kits[n]; staged != nil {
			item.Staged = &KitStagedInfo{Version: staged.Version, TreeHash: staged.TreeHash, StagedAt: staged.StagedAt}
		}
		if wl, err := kitworklist.Load(kitworklist.Path(root, n)); err == nil && wl != nil {
			open, resolved, accepted := wl.Counts()
			item.Worklist = &KitWorklistCounts{Open: open, Resolved: resolved, Accepted: accepted}
		}
		out.Kits = append(out.Kits, item)
	}
	return nil, out, nil
}

// handleKitUpdate stages an update candidate through the shared
// kitstage.Update engine. The resolver seam comes from WithKitTrialDeps —
// without it the tool refuses rather than staging bytes the CLI's resolver
// stack would not have resolved.
func (srv *Server) handleKitUpdate(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args KitUpdateArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Name == "" {
		return buildToolError(ErrBadRequest, "kit.update: name is required"), nil, nil
	}
	if srv.kitDeps.ResolveEntry == nil {
		return buildToolError(ErrKitUnavailable, "kit.update: this studio was started without kit lifecycle deps (WithKitTrialDeps); use the kitsoki CLI"), nil, nil
	}
	root, rerr := srv.resolveKitTarget(args.Target)
	if rerr != nil {
		return rerr, nil, nil
	}
	res, err := kitstage.Update(ctx, kitstage.UpdateOptions{
		ProjectRoot: root,
		Name:        args.Name,
		To:          args.To,
		Source:      args.Source,
		CheckOnly:   args.CheckOnly,
		Resolve:     srv.kitDeps.ResolveEntry,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("kit.update: %v", err)), nil, nil
	}

	out := KitUpdateOK{
		OK:       true,
		Kit:      args.Name,
		Staged:   res.Staged,
		UpToDate: res.UpToDate,
		From:     kitSnapshot(kitstage.SnapshotOfLock(res.Locked)),
		To:       kitSnapshot(res.Candidate.Snapshot()),
	}
	if res.Plan != nil {
		out.ChangedFilesCount = len(res.Plan.ChangedFiles)
		out.ChangedFilesNote = res.Plan.ChangedFilesNote
		out.RenameHintsCount = len(res.Plan.RenameHints)
		if args.Detailed {
			out.ChangedFiles = projectFileChanges(res.Plan.ChangedFiles)
			out.RenameHints = projectRenameHints(res.Plan.RenameHints)
		}
	}
	if res.PlanPath != "" {
		out.PlanPath = relToRoot(root, res.PlanPath)
	}
	return nil, out, nil
}

// handleKitTrial runs the full trial engine against the staged candidate.
// Deterministic by construction: the run is replay-strict (a cassette miss
// fails closed, recording is forbidden), exactly like the CLI verb.
func (srv *Server) handleKitTrial(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args KitTrialArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Name == "" {
		return buildToolError(ErrBadRequest, "kit.trial: name is required"), nil, nil
	}
	root, rerr := srv.resolveKitTarget(args.Target)
	if rerr != nil {
		return rerr, nil, nil
	}

	deps := srv.kitDeps
	var resolvers []app.ImportResolver
	for _, r := range []app.ImportResolver{deps.BaseResolver, deps.PlainResolver} {
		if r != nil {
			resolvers = append(resolvers, r)
		}
	}
	var projectChecks func(ctx context.Context, resolver app.ImportResolver) ([]kittrial.Check, error)
	if deps.ProjectChecks != nil {
		projectChecks = func(ctx context.Context, resolver app.ImportResolver) ([]kittrial.Check, error) {
			return deps.ProjectChecks(ctx, root, resolver)
		}
	}
	var extends kitverify.ExtendsResolver
	if deps.Extends != nil {
		extends = deps.Extends(ctx, root)
	}

	// Replay-strict for the whole trial, mirroring cmd/kitsoki: gates must
	// never record or spend silently. The env write is process-global —
	// tolerable because it only ever tightens (a concurrent tool briefly
	// seeing strict mode fails closed, never open) and is restored on exit.
	prevStrict, hadStrict := os.LookupEnv("KITSOKI_CASSETTE_STRICT")
	_ = os.Setenv("KITSOKI_CASSETTE_STRICT", "1")
	defer func() {
		if hadStrict {
			_ = os.Setenv("KITSOKI_CASSETTE_STRICT", prevStrict)
		} else {
			_ = os.Unsetenv("KITSOKI_CASSETTE_STRICT")
		}
	}()

	report, err := kittrial.Run(ctx, kittrial.Options{
		ProjectRoot:   root,
		KitName:       args.Name,
		BaseResolver:  deps.BaseResolver,
		Resolvers:     resolvers,
		Extends:       extends,
		ProjectChecks: projectChecks,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("kit.trial: %v", err)), nil, nil
	}
	return nil, projectTrialReport(root, report, args.Detailed), nil
}

// handleKitAccept promotes the staged candidate through the shared
// kittrial.Accept engine. Every fail-closed refusal (no receipt, blocked
// trial, digest drift) is a structured error the agent can act on.
func (srv *Server) handleKitAccept(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args KitAcceptArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Name == "" {
		return buildToolError(ErrBadRequest, "kit.accept: name is required"), nil, nil
	}
	root, rerr := srv.resolveKitTarget(args.Target)
	if rerr != nil {
		return rerr, nil, nil
	}
	outcome, err := kittrial.Accept(kittrial.AcceptOptions{
		ProjectRoot:  root,
		KitName:      args.Name,
		Force:        args.Force,
		AllowPartial: args.AllowPartial,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("kit.accept: %v", err)), nil, nil
	}
	return nil, KitAcceptOK{
		OK:           true,
		Kit:          args.Name,
		Version:      outcome.Staged.Version,
		TreeHash:     outcome.Staged.TreeHash,
		LockPath:     relToRoot(root, outcome.LockPath),
		ReceiptPath:  relToRoot(root, outcome.ReceiptPath),
		AcceptedWith: outcome.AcceptedWith,
	}, nil
}

// handleKitReject drops the staged candidate — residue-free by construction
// (only the staged lockfile entry and the kit-update workdir ever existed).
func (srv *Server) handleKitReject(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args KitRejectArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.Name == "" {
		return buildToolError(ErrBadRequest, "kit.reject: name is required"), nil, nil
	}
	root, rerr := srv.resolveKitTarget(args.Target)
	if rerr != nil {
		return rerr, nil, nil
	}
	f, err := kitstage.Load(kitstage.Path(root))
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	entry := f.Kits[args.Name]
	if entry == nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("kit %q has no staged candidate under %s", args.Name, root)), nil, nil
	}
	if err := kitstage.Remove(root, args.Name); err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	return nil, KitRejectOK{OK: true, Kit: args.Name, Version: entry.Version}, nil
}

// ── target resolution & projections ───────────────────────────────────────────

// resolveKitTarget maps the optional target arg to the project root the kit
// lifecycle files live under. Empty walks up from the bound workspace dir
// to the nearest .kitsoki project root (kit-lifecycle state lives at the
// project root by design, typically above a story-instance workspace);
// an absolute target is used as-is (the same override posture as story.*
// dir params); a relative target resolves under the workspace dir and is
// confined there (safeJoin).
func (srv *Server) resolveKitTarget(target string) (string, *mcpsdk.CallToolResult) {
	if target != "" && filepath.IsAbs(target) {
		return filepath.Clean(target), nil
	}
	wh, ok := srv.sess.Workspace()
	if !ok {
		return "", buildToolError(ErrNoWorkspace, "no workspace bound; open one with --workspace or pass an absolute target")
	}
	dir, _ := splitWorkspacePath(wh.Dir)
	if target != "" {
		joined, err := safeJoin(dir, target)
		if err != nil {
			return "", buildToolError(ErrBadRequest, err.Error())
		}
		return joined, nil
	}
	if root, found := findKitProjectRoot(dir); found {
		return root, nil
	}
	return dir, nil
}

// findKitProjectRoot walks up from dir to the nearest directory holding kit
// lifecycle state (an accepted or staged lockfile under .kitsoki/).
func findKitProjectRoot(dir string) (string, bool) {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if kitlock.Exists(kitlock.Path(cur)) || kitstage.Exists(kitstage.Path(cur)) {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

// projectTrialReport flattens a kittrial.Report to the wire shape: verdict +
// per-gate {id, status, cost_usd} by default; fixtures, cases, and the open
// worklist items only under detailed (token diet).
func projectTrialReport(root string, r *kittrial.Report, detailed bool) KitTrialOK {
	out := KitTrialOK{
		OK:           true,
		Kit:          r.Kit,
		Result:       r.Result,
		From:         kitSnapshot(r.From),
		To:           kitSnapshot(r.To),
		Gates:        make([]KitTrialGateItem, 0, len(r.Gates)),
		WorklistPath: relToRoot(root, r.WorklistPath),
		ReceiptPath:  relToRoot(root, r.ReceiptPath),
	}
	open, resolved, accepted := r.Worklist.Counts()
	out.Worklist = KitWorklistCounts{Open: open, Resolved: resolved, Accepted: accepted}
	for _, g := range r.Gates {
		item := KitTrialGateItem{ID: g.ID, Status: g.Status, CostUSD: g.Spend.CostUSD}
		if detailed {
			for _, fx := range g.Fixtures {
				item.Fixtures = append(item.Fixtures, KitTrialFixtureItem{
					Fixture:     fx.Fixture,
					BaselineLeg: fx.BaselineLeg,
					StagedLeg:   fx.StagedLeg,
					Failures:    fx.Failures,
				})
			}
			for _, c := range g.Cases {
				item.Cases = append(item.Cases, KitTrialCaseItem{
					CaseID:    c.CaseID,
					Status:    c.Status,
					LedgerRef: c.LedgerRef,
					Detail:    c.Detail,
				})
			}
		}
		out.Gates = append(out.Gates, item)
	}
	if detailed && r.Worklist != nil {
		for _, it := range r.Worklist.Items {
			if it.Status != kitworklist.StatusOpen {
				continue
			}
			out.OpenItems = append(out.OpenItems, KitWorklistItem{
				ID:              it.ID,
				Kind:            it.Kind,
				Severity:        it.Severity,
				Subject:         it.Subject,
				Detail:          it.Detail,
				SuggestedAction: it.SuggestedAction,
			})
		}
	}
	return out
}

func projectFileChanges(changes []kitgit.FileChange) []KitFileChange {
	out := make([]KitFileChange, 0, len(changes))
	for _, c := range changes {
		out = append(out, KitFileChange{Path: c.Path, Kind: c.Kind})
	}
	return out
}

func projectRenameHints(hints []kitstage.RenameHint) []KitRenameHint {
	out := make([]KitRenameHint, 0, len(hints))
	for _, h := range hints {
		out = append(out, KitRenameHint{
			Category:        h.Category,
			Old:             h.Old,
			New:             h.New,
			Detected:        h.Detected,
			SuggestedAction: h.SuggestedAction,
		})
	}
	return out
}

// relToRoot projects an absolute artifact path to project-relative when it
// lives under the root (paths cross the MCP wire; a project-relative path
// stays meaningful to a client on the other side of the transport).
func relToRoot(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil && rel != ".." && !filepath.IsAbs(rel) && !hasDotDotPrefix(rel) {
		return rel
	}
	return path
}

func hasDotDotPrefix(rel string) bool {
	return rel == ".." || (len(rel) > 2 && rel[:3] == ".."+string(filepath.Separator))
}
