package graph

import (
	"errors"
	"fmt"
	"os"
	"time"

	"kitsoki/internal/clock"
)

// ProposeInput is the payload for Propose: a candidate changeset's title and
// raw operations — the same wire shape ParseChangeset expects off a
// changeset node's Fields["operations"] (a list of {"kind": ..., ...}
// mappings) — plus optional system-authorship provenance.
type ProposeInput struct {
	Title      string
	Visibility string // defaults to "internal"
	Operations []map[string]any

	// Provenance, when non-nil, marks this as a system-authored changeset
	// (materialization write-back, derived status flips, ...) and typically
	// carries {"job_id": ..., "story": ...}. Combined with the D9
	// auto-authorize allowlist (autoAuthorizeFieldRoots), it determines
	// whether Propose immediately authorizes the changeset (§3.3
	// "machine-write policy: system-authored changesets ... auto-authorized
	// for an allowlisted field set") instead of leaving it "proposed" for
	// human review.
	Provenance map[string]any

	// ValidateOnly, when true, runs the full ValidateChangeset check plus a
	// scratch-copy candidate build and lint (hazard guard #5, plan §3.4
	// red-team amendment #5) but writes nothing — not even the changeset
	// node itself. Returned ChangesetID/Status/GuardFills describe what
	// WOULD happen; the id is not actually reserved.
	ValidateOnly bool
}

// ProposeResult reports the outcome of Propose.
type ProposeResult struct {
	// ChangesetID is set only when the changeset node was successfully
	// appended to the catalog.
	ChangesetID NodeID
	// Status is the changeset's resulting lifecycle status ("proposed", or
	// "authorized" when Provenance qualified for auto-authorize).
	Status string
	// Lint is the (normally empty) result of linting the catalog with the
	// new changeset node appended — appending a changeset node never
	// changes any other node, so this is mostly a sanity signal, but is
	// still gate-worthy per the op's contract (§3.3: "return
	// {changeset_id, lint}").
	Lint []LintIssue
	// RejectReasons is non-empty when the payload's operations failed
	// ValidateChangeset against the current catalog (including the
	// immediate Before stale-check) — the real catalog was never touched.
	RejectReasons []string
	// GuardFills lists every precondition Propose filled in on the
	// caller's behalf (hazard guard #5): a "modified" change that omitted
	// `before` got it filled from the live node (echoed as Node/Path/
	// Value); a "removed" or "retyped" op that omitted `before` got the
	// live node's full mapping filled in (echoed as Node/SHA/Fields, not
	// the full content). Empty when every operation already carried
	// explicit preconditions.
	GuardFills []GuardFill
	// ValidatedOnly is true when Provenance-free ProposeInput.ValidateOnly
	// was set: validation + scratch lint ran, but nothing was written, not
	// even the changeset node.
	ValidatedOnly bool
}

// autoAuthorizeFieldRoots is the D9 allowlist: a system-authored changeset
// (one carrying Provenance) whose every "modified" operation only ever
// touches paths rooted at one of these — or the bare ["status"] path, for
// derived status flips — self-authorizes instead of waiting in the human
// review queue. Any other operation kind, or any field outside this set,
// keeps the changeset "proposed" regardless of provenance. Human-authored
// content changesets (no Provenance) always stay "proposed".
var autoAuthorizeFieldRoots = map[string]bool{
	"materialization": true,
	"evidence":        true,
}

func isAutoAuthorizablePath(path []string, roots map[string]bool) bool {
	if len(path) == 1 && path[0] == "status" {
		return true
	}
	if len(path) == 2 && path[0] == "fields" {
		return roots[path[1]]
	}
	return false
}

// allOpsAutoAuthorizable reports whether every operation in ops is a
// "modified" op whose every field change targets an allowlisted path (D9).
// roots is the effective allowlist: cat.WritePolicyOrDefault()'s declared
// auto_authorize_field_roots, or the hardcoded autoAuthorizeFieldRoots
// fallback when the catalog declares no write_policy block.
func allOpsAutoAuthorizable(ops []Operation, roots map[string]bool) bool {
	if len(ops) == 0 {
		return false
	}
	for _, op := range ops {
		if op.Kind != OpModified {
			return false
		}
		for _, ch := range op.Changes {
			if !isAutoAuthorizablePath(ch.Path, roots) {
				return false
			}
		}
	}
	return true
}

// writePolicyRootsSet resolves cat's effective auto-authorize allowlist
// (D9 generalization, plan §3.4 item 3) into the map shape
// isAutoAuthorizablePath/allOpsAutoAuthorizable consult.
func writePolicyRootsSet(cat *Catalog) map[string]bool {
	policy := cat.WritePolicyOrDefault()
	roots := make(map[string]bool, len(policy.AutoAuthorizeFieldRoots))
	for _, r := range policy.AutoAuthorizeFieldRoots {
		roots[r] = true
	}
	return roots
}

// rawOpsToAny adapts the wire shape ([]map[string]any) Propose's callers
// hand in to the []any ParseChangeset expects off Fields["operations"] (it
// type-asserts raw.([]any), then each element to map[string]any).
func rawOpsToAny(ops []map[string]any) []any {
	out := make([]any, len(ops))
	for i, op := range ops {
		out[i] = op
	}
	return out
}

// nextChangesetID picks a fresh, collision-free "cs-<n>" id — the simplest
// deterministic scheme that survives concurrent-ish propose calls within a
// single process (each call reloads the catalog fresh).
func nextChangesetID(cat *Catalog) NodeID {
	n := 0
	for _, node := range cat.Nodes {
		if node.TypeID == "changeset" {
			n++
		}
	}
	for {
		n++
		candidate := NodeID(fmt.Sprintf("cs-%d", n))
		if _, exists := cat.Nodes[candidate]; !exists {
			return candidate
		}
	}
}

// Propose validates a changeset payload's operations against rootPath's
// current catalog — via ParseChangeset + ValidateChangeset, exactly the
// checks Apply itself runs, so a Before stale-check or a "node already
// exists" problem surfaces immediately, not at authorize/apply time — then
// assigns a fresh changeset id and appends the changeset node itself
// (comment-preserving, via the same scratch-copy + applyOperations + Lint
// machinery Apply uses) to the catalog. A rejected propose never touches
// rootPath.
//
// Nothing else may write a changeset node into the catalog: Apply requires
// it to already exist (apply.go's Apply loads csNode and errors if absent),
// and flipping a changeset's own status via a changeset would be infinite
// regress — so Propose is the sole writer of new changeset nodes, and
// Authorize (below) is the sole writer of the proposed->authorized flip.
//
// actor and clk are the D9-generalization-adjacent stamps seam (plan §3.4):
// actor (optional — "" skips the authored_by stamp) and clk (nil defaults
// to clock.Real()) stamp the new changeset node's Fields with created_at
// (always, RFC3339 UTC) and authored_by (when actor is non-empty), with
// zero type_registry/schema migration — see Node.Fields' free-form-map
// doc comment.
func Propose(rootPath string, input ProposeInput, actor string, clk clock.Clock) (*ProposeResult, error) {
	if clk == nil {
		clk = clock.Real()
	}
	// File-level CAS (hazard guard #2): each attempt does a fresh
	// LoadCatalog + id allocation; a concurrent writer landing in the
	// window is detected right before copy-back and the whole attempt
	// (including cs-<n> allocation) is redone against a fresh load.
	for attempt := 1; attempt <= casMaxAttempts; attempt++ {
		res, retry, err := proposeOnce(rootPath, input, actor, clk)
		if retry {
			continue
		}
		return res, err
	}
	return &ProposeResult{RejectReasons: []string{
		fmt.Sprintf("CONFLICT: %s changed concurrently during propose, exceeded %d retry attempts", rootPath, casMaxAttempts),
	}}, nil
}

func proposeOnce(rootPath string, input ProposeInput, actor string, clk clock.Clock) (res *ProposeResult, retry bool, err error) {
	cat, err := LoadCatalog(rootPath)
	if err != nil {
		return nil, false, fmt.Errorf("graph propose: load %s: %w", rootPath, err)
	}
	// Canonicality pre-check (hazard guard #4): before any write attempt.
	if reasons := checkCanonical(cat); len(reasons) > 0 {
		return &ProposeResult{RejectReasons: reasons}, false, nil
	}

	synthetic := &Node{
		ID:     "__propose-candidate__",
		TypeID: "changeset",
		Fields: map[string]any{"operations": rawOpsToAny(input.Operations)},
	}
	cs, err := ParseChangeset(synthetic)
	if err != nil {
		return &ProposeResult{RejectReasons: []string{err.Error()}}, false, nil
	}

	// Guard-fill (hazard guard #5): a "modified" change or a "removed"/
	// "retyped" op that omitted its precondition gets it filled from the live node,
	// mutating cs.Operations in place — the changeset persisted below
	// carries the filled preconditions, not the caller's bare payload.
	guardFills := fillGuards(cs, cat)

	if reasons := ValidateChangeset(cs, cat); len(reasons) > 0 {
		return &ProposeResult{RejectReasons: reasons}, false, nil
	}

	id := nextChangesetID(cat)
	visibility := input.Visibility
	if visibility == "" {
		visibility = "internal"
	}
	status := ChangesetStatusProposed
	if input.Provenance != nil && allOpsAutoAuthorizable(cs.Operations, writePolicyRootsSet(cat)) {
		status = ChangesetStatusAuthorized
	}

	filledOps := rawOpsToAny(opsToRaw(cs.Operations))
	nodeMap := map[string]any{
		"schema":     "graph/changeset/v1",
		"id":         string(id),
		"title":      input.Title,
		"status":     status,
		"visibility": visibility,
		"operations": filledOps,
		"created_at": clk.Now().UTC().Format(time.RFC3339),
	}
	if actor != "" {
		nodeMap["authored_by"] = actor
	}
	if input.Provenance != nil {
		nodeMap["provenance"] = input.Provenance
	}

	addOp := []Operation{{Kind: OpAdded, Node: id, After: nodeMap}}

	if input.ValidateOnly {
		_, issues, rejectReasons, err := commitScratchOperations(cat, addOp, true /* dryRun */)
		if err != nil {
			if errors.Is(err, errCASConflict) {
				return nil, true, nil
			}
			return nil, false, fmt.Errorf("graph propose: %w", err)
		}
		if len(rejectReasons) > 0 {
			return &ProposeResult{RejectReasons: rejectReasons}, false, nil
		}
		return &ProposeResult{ChangesetID: id, Status: status, Lint: issues, GuardFills: guardFills, ValidatedOnly: true}, false, nil
	}

	changedFiles, issues, rejectReasons, err := commitScratchOperations(cat, addOp, false)
	if err != nil {
		if errors.Is(err, errCASConflict) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("graph propose: %w", err)
	}
	if len(rejectReasons) > 0 {
		return &ProposeResult{RejectReasons: rejectReasons}, false, nil
	}
	if len(issues) > 0 {
		return &ProposeResult{Lint: issues}, false, nil
	}
	_ = changedFiles

	return &ProposeResult{ChangesetID: id, Status: status, Lint: issues, GuardFills: guardFills}, false, nil
}

// Authorize flips a changeset from "proposed" to "authorized" — the only
// mutation lifecycle flips make (§3.3: "content writes are changesets;
// lifecycle flips are blessed direct writes with provenance"). It writes
// directly rather than through another changeset: flipping a changeset's
// own status via a changeset would be infinite regress (see Propose's doc
// comment). On an unauthenticated localhost /rpc this is honor-system.
//
// actor and clk stamp the flip's authorized_at (always, RFC3339 UTC) and
// authorized_by (when actor is non-empty) into the changeset node's Fields
// map, alongside the status change, in the same commit — see Propose's doc
// comment for the stamps seam.
func Authorize(rootPath string, changesetID NodeID, actor string, clk clock.Clock) (*ApplyResult, error) {
	if clk == nil {
		clk = clock.Real()
	}
	for attempt := 1; attempt <= casMaxAttempts; attempt++ {
		res, retry, err := authorizeOnce(rootPath, changesetID, actor, clk)
		if retry {
			continue
		}
		return res, err
	}
	return &ApplyResult{RejectReasons: []string{
		fmt.Sprintf("CONFLICT: %s changed concurrently during authorize, exceeded %d retry attempts", rootPath, casMaxAttempts),
	}}, nil
}

func authorizeOnce(rootPath string, changesetID NodeID, actor string, clk clock.Clock) (res *ApplyResult, retry bool, err error) {
	cat, err := LoadCatalog(rootPath)
	if err != nil {
		return nil, false, fmt.Errorf("graph authorize: load %s: %w", rootPath, err)
	}
	if reasons := checkCanonical(cat); len(reasons) > 0 {
		return &ApplyResult{RejectReasons: reasons}, false, nil
	}
	node, ok := cat.Nodes[changesetID]
	if !ok {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph authorize: changeset %q not found in catalog", changesetID),
		}}, false, nil
	}
	if node.TypeID != "changeset" {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph authorize: node %q is type %q, not changeset", changesetID, node.TypeID),
		}}, false, nil
	}
	if node.Status != ChangesetStatusProposed {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph authorize: changeset %q has status %q, must be %q to authorize", changesetID, node.Status, ChangesetStatusProposed),
		}}, false, nil
	}

	changes := []FieldChange{
		{Path: []string{"status"}, Before: ChangesetStatusProposed, After: ChangesetStatusAuthorized},
		{Path: []string{"fields", "authorized_at"}, After: clk.Now().UTC().Format(time.RFC3339)},
	}
	if actor != "" {
		changes = append(changes, FieldChange{Path: []string{"fields", "authorized_by"}, After: actor})
	}
	changedFiles, issues, rejectReasons, err := commitScratchOperations(cat, []Operation{
		{
			Kind:    OpModified,
			Node:    changesetID,
			Changes: changes,
		},
	}, false)
	if err != nil {
		if errors.Is(err, errCASConflict) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("graph authorize: %w", err)
	}
	if len(rejectReasons) > 0 {
		return &ApplyResult{RejectReasons: rejectReasons}, false, nil
	}
	if len(issues) > 0 {
		return &ApplyResult{LintIssues: issues}, false, nil
	}
	return &ApplyResult{Applied: true, ChangedFiles: changedFiles}, false, nil
}

// Withdraw flips a "proposed" or "authorized" (but not yet applied/
// "notified") changeset to "withdrawn" — the review queue's "clean up a
// rejected proposal" action (§3.3). Like Authorize, this is a direct write,
// not a second changeset.
//
// actor and clk are accepted for seam consistency with Propose/Authorize/
// Apply (plan §3.4 item 1) but currently unused: withdraw stamps no
// fields per the plan's stamp list (created_at/authored_by,
// authorized_at/authorized_by, applied_at only).
func Withdraw(rootPath string, changesetID NodeID, actor string, clk clock.Clock) (*ApplyResult, error) {
	_, _ = actor, clk
	for attempt := 1; attempt <= casMaxAttempts; attempt++ {
		res, retry, err := withdrawOnce(rootPath, changesetID)
		if retry {
			continue
		}
		return res, err
	}
	return &ApplyResult{RejectReasons: []string{
		fmt.Sprintf("CONFLICT: %s changed concurrently during withdraw, exceeded %d retry attempts", rootPath, casMaxAttempts),
	}}, nil
}

func withdrawOnce(rootPath string, changesetID NodeID) (res *ApplyResult, retry bool, err error) {
	cat, err := LoadCatalog(rootPath)
	if err != nil {
		return nil, false, fmt.Errorf("graph withdraw: load %s: %w", rootPath, err)
	}
	if reasons := checkCanonical(cat); len(reasons) > 0 {
		return &ApplyResult{RejectReasons: reasons}, false, nil
	}
	node, ok := cat.Nodes[changesetID]
	if !ok {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph withdraw: changeset %q not found in catalog", changesetID),
		}}, false, nil
	}
	if node.TypeID != "changeset" {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph withdraw: node %q is type %q, not changeset", changesetID, node.TypeID),
		}}, false, nil
	}
	if node.Status != ChangesetStatusProposed && node.Status != ChangesetStatusAuthorized {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph withdraw: changeset %q has status %q, must be %q or %q to withdraw", changesetID, node.Status, ChangesetStatusProposed, ChangesetStatusAuthorized),
		}}, false, nil
	}

	changedFiles, issues, rejectReasons, err := commitScratchOperations(cat, []Operation{
		{
			Kind: OpModified,
			Node: changesetID,
			Changes: []FieldChange{
				{Path: []string{"status"}, Before: node.Status, After: ChangesetStatusWithdrawn},
			},
		},
	}, false)
	if err != nil {
		if errors.Is(err, errCASConflict) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("graph withdraw: %w", err)
	}
	if len(rejectReasons) > 0 {
		return &ApplyResult{RejectReasons: rejectReasons}, false, nil
	}
	if len(issues) > 0 {
		return &ApplyResult{LintIssues: issues}, false, nil
	}
	return &ApplyResult{Applied: true, ChangedFiles: changedFiles}, false, nil
}

// Rebase refreshes a "proposed" changeset's stale Before guards (§3.3's
// review-queue "rebase" action, cut from A1) against the catalog's current
// state, then re-validates. For every "modified" operation's field changes,
// a Before that no longer matches the live catalog is refreshed to the live
// value (via refreshStaleGuards, changeset.go — fillGuards' sibling, sharing
// its readNodePath/valuesEqual comparison-canonical reads but overwriting a
// present-but-stale Before rather than only filling a missing one); the
// changeset is only actually rewritten if, after refreshing,
// ValidateChangeset reports zero remaining reasons — i.e. the refresh was
// non-conflicting (nothing else about the operation, e.g. a missing node,
// was wrong). If a genuine conflict remains, Rebase leaves the changeset
// untouched and reports the reasons, exactly like a rejected Propose. This
// intentionally does not touch "removed"/"retyped"/other op kinds' guards —
// only "modified" field-level Before values, which is what the review queue
// scenario (someone else edited an unrelated field while your changeset sat
// in the queue) actually needs.
//
// actor and clk are accepted for seam consistency with Propose/Authorize/
// Withdraw/Apply (plan §3.4 item 1) but currently unused, exactly like
// Withdraw: Rebase stamps no fields of its own (no "rebased_at" in the
// plan's stamp list) — it only rewrites the changeset's operations field, so
// created_at/authored_by (and authorized_at/authorized_by, if already
// authorized-then-withdrawn-back-to-proposed) are untouched and survive.
func Rebase(rootPath string, changesetID NodeID, actor string, clk clock.Clock) (*ApplyResult, error) {
	_, _ = actor, clk
	// File-level CAS (hazard guard #2), same bounded retry shape as
	// Propose/Authorize/Withdraw/Apply.
	for attempt := 1; attempt <= casMaxAttempts; attempt++ {
		res, retry, err := rebaseOnce(rootPath, changesetID)
		if retry {
			continue
		}
		return res, err
	}
	return &ApplyResult{RejectReasons: []string{
		fmt.Sprintf("CONFLICT: %s changed concurrently during rebase, exceeded %d retry attempts", rootPath, casMaxAttempts),
	}}, nil
}

func rebaseOnce(rootPath string, changesetID NodeID) (res *ApplyResult, retry bool, err error) {
	cat, err := LoadCatalog(rootPath)
	if err != nil {
		return nil, false, fmt.Errorf("graph rebase: load %s: %w", rootPath, err)
	}
	// Canonicality pre-check (hazard guard #4): before any write attempt.
	if reasons := checkCanonical(cat); len(reasons) > 0 {
		return &ApplyResult{RejectReasons: reasons}, false, nil
	}
	node, ok := cat.Nodes[changesetID]
	if !ok {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph rebase: changeset %q not found in catalog", changesetID),
		}}, false, nil
	}
	if node.TypeID != "changeset" {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph rebase: node %q is type %q, not changeset", changesetID, node.TypeID),
		}}, false, nil
	}
	if node.Status != ChangesetStatusProposed {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph rebase: changeset %q has status %q, must be %q to rebase", changesetID, node.Status, ChangesetStatusProposed),
		}}, false, nil
	}

	rawOps, ok := node.Fields["operations"].([]any)
	if !ok {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph rebase: changeset %q missing operations", changesetID),
		}}, false, nil
	}

	cs, err := ParseChangeset(node)
	if err != nil {
		return &ApplyResult{RejectReasons: []string{err.Error()}}, false, nil
	}

	if !refreshStaleGuards(cs, cat) {
		return &ApplyResult{RejectReasons: []string{
			fmt.Sprintf("graph rebase: changeset %q has no stale Before guards to refresh", changesetID),
		}}, false, nil
	}

	// Re-validate the refreshed operations before committing anything —
	// only a fully non-conflicting refresh is applied.
	if reasons := ValidateChangeset(cs, cat); len(reasons) > 0 {
		return &ApplyResult{RejectReasons: reasons}, false, nil
	}

	newRawOps := rawOpsToAny(opsToRaw(cs.Operations))
	changedFiles, issues, rejectReasons, err := commitScratchOperations(cat, []Operation{
		{
			Kind: OpModified,
			Node: changesetID,
			Changes: []FieldChange{
				{Path: []string{"fields", "operations"}, Before: rawOps, After: newRawOps},
			},
		},
	}, false)
	if err != nil {
		if errors.Is(err, errCASConflict) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("graph rebase: %w", err)
	}
	if len(rejectReasons) > 0 {
		return &ApplyResult{RejectReasons: rejectReasons}, false, nil
	}
	if len(issues) > 0 {
		return &ApplyResult{LintIssues: issues}, false, nil
	}
	return &ApplyResult{Applied: true, ChangedFiles: changedFiles}, false, nil
}

// commitScratchOperations is Apply's dry-run-first scratch-copy machinery,
// factored out so Propose and Authorize can commit a small synthetic
// operation set (appending a changeset node, or flipping its status) with
// the exact same comment-preserving-rewrite-then-lint-then-copy-back
// guarantee Apply gives ordinary changesets — a rejected candidate never
// touches rootPath. dryRun mirrors Apply's own dry-run convention
// (ValidateOnly Propose calls): builds and lints the scratch candidate but
// never calls commitWithCAS.
//
// The lint gate only blocks on error-severity issues NOT already present
// in cat's own baseline lint (hazard guard #3: pre-existing catalog dirt
// must never deadlock a write), and commit-time uses commitWithCAS (hazard
// guard #2): a content-digest mismatch returns errCASConflict for the
// caller's retry loop instead of clobbering a concurrent write.
func commitScratchOperations(cat *Catalog, ops []Operation, dryRun bool) (changedFiles []string, lintIssues []LintIssue, rejectReasons []string, err error) {
	baseline := ErrorIssues(Lint(cat))

	tmpDir, err := os.MkdirTemp("", "kitsoki-graph-commit-*")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("mktemp: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	scratchRoot, err := copyCatalogTree(cat.RootPath, tmpDir, cat)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("copy scratch tree: %w", err)
	}

	changed, err := applyOperations(cat, ops, scratchRoot)
	if err != nil {
		return nil, nil, []string{err.Error()}, nil
	}

	candidate, err := LoadCatalog(scratchRoot)
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("candidate catalog failed to load: %v", err)}, nil
	}
	// Error-severity only, same rationale as Apply's gate (apply.go):
	// advisory warnings must not block a propose/commit. Only NEW issues
	// (not in baseline) block (hazard guard #3).
	if issues := newErrorIssues(baseline, ErrorIssues(Lint(candidate))); len(issues) > 0 {
		return nil, issues, nil, nil
	}

	if dryRun {
		return changed, nil, nil, nil
	}

	if err := commitWithCAS(cat, scratchRoot, changed); err != nil {
		return nil, nil, nil, err
	}
	return changed, nil, nil, nil
}
