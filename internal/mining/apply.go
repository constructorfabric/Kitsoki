package mining

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// Reloader is the meta-mode reload seam, satisfied by *orchestrator.Orchestrator
// in production (Reload + RerunOnEnter, world preserved) and a fake in tests.
// The apply gate drives it after writing the staged delta and again after a
// revert. Implementations MUST be safe against a concurrent turn only when the
// caller holds the per-chat lock (see Apply's Lock).
type Reloader interface {
	// Reload re-reads the app at appPath, rebuilds the machine, and swaps it in.
	// prevState lets the implementation report whether the operator's state
	// survives the swap; the mining gate ignores that (the world is preserved).
	Reload(appPath string, prevState app.StatePath) error
	// RerunOnEnter re-fires the entered state's on_enter chain against the live
	// world so the reloaded structure takes effect without a re-typed intent.
	RerunOnEnter(ctx context.Context, sid app.SessionID) error
}

// FlowGate runs the no-LLM flow suite and reports the failure count. In
// production this wraps testrunner.RunFlows (the same gate kitsoki test flows
// uses); a non-zero Failed reverts the edit. Injected so the package never pulls
// the flow runner into its own import graph.
type FlowGate interface {
	// RunFlows runs every flow fixture matching glob against the app at appPath
	// and returns the number that failed (0 == green) or an error if the suite
	// could not run at all.
	RunFlows(ctx context.Context, appPath, glob string) (failed int, err error)
}

// Locker is the per-chat mutual-exclusion the TUI edit-mode uses to keep a
// reload from racing a turn in flight (reload is not concurrency-safe with a
// turn). The apply gate acquires it before writing the delta and releases it
// after the keep-or-revert is total.
type Locker interface {
	Lock()
	Unlock()
}

// Applier is the accept gate. On accept it writes the staged delta onto the live
// tree, drives the reload seam, runs the flow gate, and keeps-on-green or
// reverts-and-holds-on-red — recording the verdict as MiningProposalDecided. It
// is constructed per-chat with the live tree root + entry, the session, and the
// injected seams.
type Applier struct {
	// TreeRoot is the live tree's root directory (the dir the relpaths in a
	// Proposal.Files are relative to, and the root Reload reads the entry under).
	TreeRoot string
	// Entry is the app entry-manifest path relative to TreeRoot (passed to
	// Reload as appPath = filepath.Join(TreeRoot, Entry)).
	Entry string
	// State is the operator's current state path (passed to Reload; the world is
	// preserved regardless).
	State app.StatePath
	// SID is the session the reload + events belong to.
	SID app.SessionID
	// FlowGlob selects which fixtures the gate runs (e.g. "flows/*.yaml").
	FlowGlob string

	// Seams (DI):
	Reloader Reloader
	FlowGate FlowGate
	// Lock is the per-chat reload guard. Optional; nil skips locking (single-
	// threaded callers / tests that hold no concurrent turn).
	Lock Locker
}

// ApplyResult reports the outcome of an accept.
type ApplyResult struct {
	// FlowsGreen is the gate result (true == kept live).
	FlowsGreen bool
	// Reverted is true when a red gate rolled the edit back byte-for-byte.
	Reverted bool
	// FailedFlows is the gate's failure count (0 when green).
	FailedFlows int
}

// appPath is the absolute-ish entry path Reload reads.
func (a *Applier) appPath() string { return filepath.Join(a.TreeRoot, a.Entry) }

// flowGlob resolves FlowGlob against the tree root unless it is already
// absolute. testrunner.RunFlows passes the glob straight to filepath.Glob, so a
// relative glob would otherwise resolve against the process cwd (not the live
// tree) and match nothing.
func (a *Applier) flowGlob() string {
	if a.FlowGlob == "" {
		return filepath.Join(a.TreeRoot, "flows", "*.yaml")
	}
	if filepath.IsAbs(a.FlowGlob) {
		return a.FlowGlob
	}
	return filepath.Join(a.TreeRoot, a.FlowGlob)
}

// Accept applies a proposal's delta along the meta-mode reload path, gated on
// the no-LLM flow suite staying green:
//
//  1. snapshot the bytes of every file the delta touches (for byte-for-byte
//     revert);
//  2. write the delta onto the live tree;
//  3. Reload + RerunOnEnter (world preserved);
//  4. run the flow gate;
//  5. KEEP on green; on red restore the snapshot, re-Reload, and HOLD the
//     proposal with the failure attached.
//
// Either way it records MiningProposalDecided{verdict:accept, flows_green,
// reverted} via sink. The accept is human-only in v1 (by=human); an LLM judge
// is out of scope.
func (a *Applier) Accept(ctx context.Context, prop *Proposal, sink *SessionSink) (*ApplyResult, error) {
	if a.Reloader == nil || a.FlowGate == nil {
		return nil, fmt.Errorf("mining: applier requires a Reloader and a FlowGate")
	}
	if prop == nil || len(prop.Files) == 0 {
		return nil, fmt.Errorf("mining: accept needs a proposal with staged files")
	}

	// Acquire the same reload guard the TUI edit-mode uses so a meta turn can't
	// race a drive. Held across the whole keep-or-revert so the state is never
	// half-applied from a concurrent turn's view.
	if a.Lock != nil {
		a.Lock.Lock()
		defer a.Lock.Unlock()
	}

	// (1) snapshot the pre-edit bytes of every touched file (nil == absent →
	// remove on revert).
	snap := a.snapshot(prop.Files)

	// (2) write the delta onto the live tree.
	if err := a.writeFiles(prop.Files); err != nil {
		// Best-effort restore so a partial write never survives.
		_ = a.restore(snap)
		return nil, fmt.Errorf("mining: write delta: %w", err)
	}

	// (3) reload + rerun on_enter (world preserved). A reload failure is itself
	// a regression (the edited tree won't load) — treat it exactly like a red
	// gate and revert. The flow gate never runs.
	if err := a.reload(ctx); err != nil {
		return a.revertAndHold(ctx, sink, prop.Recipe.ID, snap, 0)
	}

	// (4) the gate: run the no-LLM flow suite against the reloaded tree.
	failed, gerr := a.FlowGate.RunFlows(ctx, a.appPath(), a.flowGlob())
	if gerr != nil {
		// Could not run the suite at all — treat as red and revert.
		return a.revertAndHold(ctx, sink, prop.Recipe.ID, snap, 0)
	}
	if failed > 0 {
		// (5-red) REVERT byte-for-byte + re-Reload + HOLD the proposal.
		return a.revertAndHold(ctx, sink, prop.Recipe.ID, snap, failed)
	}

	// (5-green) KEEP live.
	res := &ApplyResult{FlowsGreen: true, Reverted: false}
	return res, a.recordAccept(sink, prop.Recipe.ID, res, nil)
}

// Refine records a refine verdict. The actual editing surface is the builtin
// story.edit meta mode with the draft preloaded (driven by the caller /
// mine-command-ux); this records the recorded interpretive decision.
func (a *Applier) Refine(recipeID string, sink *SessionSink) error {
	return appendPayload(sink, store.MiningProposalDecided, store.MiningProposalDecidedPayload{
		RecipeID: recipeID,
		Verdict:  store.MiningVerdictRefine,
		By:       store.MiningByHuman,
	})
}

// Reject records a rejected proposal — the negative that keeps the recipe from
// re-surfacing (mine-command-ux reads it to suppress).
func (a *Applier) Reject(recipeID string, sink *SessionSink) error {
	return appendPayload(sink, store.MiningProposalDecided, store.MiningProposalDecidedPayload{
		RecipeID: recipeID,
		Verdict:  store.MiningVerdictReject,
		By:       store.MiningByHuman,
	})
}

// recordAccept appends the accept verdict. A non-nil cause is wrapped onto the
// returned error so the caller can surface why a green-gate failure reverted.
func (a *Applier) recordAccept(sink *SessionSink, recipeID string, res *ApplyResult, cause error) error {
	if aerr := appendPayload(sink, store.MiningProposalDecided, store.MiningProposalDecidedPayload{
		RecipeID:   recipeID,
		Verdict:    store.MiningVerdictAccept,
		By:         store.MiningByHuman,
		FlowsGreen: res.FlowsGreen,
		Reverted:   res.Reverted,
	}); aerr != nil {
		if cause != nil {
			return fmt.Errorf("%w (and recording the verdict failed: %v)", cause, aerr)
		}
		return fmt.Errorf("mining: append MiningProposalDecided: %w", aerr)
	}
	return cause
}

// revertAndHold restores the pre-edit snapshot byte-for-byte, re-Reloads to the
// original, and records MiningProposalDecided{accept, flows_green:false,
// reverted:true} — the proposal is HELD (not consumed) so the surface can
// re-offer it with the failure attached. A clean revert returns nil error; only
// a FAILED revert (which would leave the live app in a half-applied state — an
// invariant violation) returns an error. failed is the gate's failure count
// (0 when the regression was a reload failure rather than a flow miss).
func (a *Applier) revertAndHold(ctx context.Context, sink *SessionSink, recipeID string, snap []fileSnapshot, failed int) (*ApplyResult, error) {
	if rerr := a.restore(snap); rerr != nil {
		return nil, fmt.Errorf("mining: gate red and revert FAILED (live tree may be inconsistent): %w", rerr)
	}
	if rerr := a.reload(ctx); rerr != nil {
		return nil, fmt.Errorf("mining: gate red, reverted, but re-Reload FAILED: %w", rerr)
	}
	res := &ApplyResult{FlowsGreen: false, Reverted: true, FailedFlows: failed}
	return res, a.recordAccept(sink, recipeID, res, nil)
}

func (a *Applier) reload(ctx context.Context) error {
	if err := a.Reloader.Reload(a.appPath(), a.State); err != nil {
		return err
	}
	return a.Reloader.RerunOnEnter(ctx, a.SID)
}

// fileSnapshot is the pre-edit byte image of one touched file. present=false
// means the file did not exist (revert removes it).
type fileSnapshot struct {
	rel     string
	present bool
	bytes   []byte
}

// snapshot captures the pre-edit bytes of every file the delta will touch, in a
// deterministic order. Files absent before the edit are recorded as not-present
// so a revert removes them.
func (a *Applier) snapshot(files map[string][]byte) []fileSnapshot {
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	out := make([]fileSnapshot, 0, len(rels))
	for _, rel := range rels {
		abs := filepath.Join(a.TreeRoot, filepath.FromSlash(rel))
		b, err := os.ReadFile(abs)
		if err != nil {
			out = append(out, fileSnapshot{rel: rel, present: false})
			continue
		}
		cp := make([]byte, len(b))
		copy(cp, b)
		out = append(out, fileSnapshot{rel: rel, present: true, bytes: cp})
	}
	return out
}

// writeFiles writes the delta onto the live tree.
func (a *Applier) writeFiles(files map[string][]byte) error {
	rels := make([]string, 0, len(files))
	for rel := range files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		abs := filepath.Join(a.TreeRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("mkdir %q: %w", abs, err)
		}
		if err := os.WriteFile(abs, files[rel], 0o644); err != nil {
			return fmt.Errorf("write %q: %w", abs, err)
		}
	}
	return nil
}

// restore rolls every snapshotted file back to its pre-edit bytes byte-for-byte
// (removing files that did not exist before). Invariant: a regressing edit never
// survives the turn — keep-or-revert is total.
func (a *Applier) restore(snap []fileSnapshot) error {
	for _, fs := range snap {
		abs := filepath.Join(a.TreeRoot, filepath.FromSlash(fs.rel))
		if !fs.present {
			if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("restore (remove) %q: %w", abs, err)
			}
			continue
		}
		if err := os.WriteFile(abs, fs.bytes, 0o644); err != nil {
			return fmt.Errorf("restore (write) %q: %w", abs, err)
		}
	}
	return nil
}
