package kittrial

// The `kit accept` promotion engine, factored out of cmd/kitsoki so the CLI
// verb and the studio MCP kit.accept tool share one fail-closed behaviour.
// Accept promotes a staged candidate into kits.lock only when a trial
// receipt exists for the EXACT staged tree, its result is not blocked
// (partial additionally needs AllowPartial, recorded into the acceptance
// receipt), and every source digest the receipt pinned still matches — any
// drift means the trial no longer describes reality and must be re-run.

import (
	"fmt"
	"path/filepath"
	"time"

	"kitsoki/internal/kitlock"
	"kitsoki/internal/kitstage"
)

// AcceptOptions configures one promotion run.
type AcceptOptions struct {
	// ProjectRoot is the project whose staged candidate is promoted.
	ProjectRoot string
	// KitName is the staged kit to accept.
	KitName string
	// Force skips the trial-receipt gate entirely (recorded in the
	// acceptance receipt as accepted_with: force — an audit trail, never a
	// silent bypass).
	Force bool
	// AllowPartial accepts a partial trial (pending approvals / warnings);
	// recorded in the receipt as accepted_with: partial.
	AllowPartial bool
	// ArtifactsRoot is where trial receipts live; defaults to
	// <ProjectRoot>/.artifacts/kit-trial (matching kittrial.Run).
	ArtifactsRoot string
	// Now is injectable for deterministic tests; defaults to time.Now.
	Now func() time.Time
}

// AcceptOutcome reports a successful promotion.
type AcceptOutcome struct {
	// Staged is the candidate entry that was promoted.
	Staged *kitstage.Entry
	// LockPath is the accepted lockfile that now pins the candidate.
	LockPath string
	// ReceiptPath is the durable acceptance receipt (committed — it
	// travels with the lock it justifies).
	ReceiptPath string
	// AcceptedWith records a non-ready accept: "" (clean), "partial", or
	// "force".
	AcceptedWith string
}

// Accept promotes the staged candidate for opts.KitName into kits.lock,
// fail-closed on the trial receipt unless opts.Force. The write order is
// (a) durable acceptance receipt, (b) lock promotion, (c) staging cleanup —
// so every crash window is detectable (staged==accepted) and recoverable.
func Accept(opts AcceptOptions) (*AcceptOutcome, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ArtifactsRoot == "" {
		opts.ArtifactsRoot = filepath.Join(opts.ProjectRoot, ".artifacts", "kit-trial")
	}
	name := opts.KitName

	sf, err := kitstage.Load(kitstage.Path(opts.ProjectRoot))
	if err != nil {
		return nil, err
	}
	staged := sf.Kits[name]
	if staged == nil {
		return nil, fmt.Errorf("kit %q has no staged candidate under %s — run `kitsoki kit update %s` first", name, opts.ProjectRoot, name)
	}
	lockPath := kitlock.Path(opts.ProjectRoot)
	lf, err := kitlock.Load(lockPath)
	if err != nil {
		return nil, err
	}
	locked := lf.Kits[name]

	instances := kitstage.InstanceApps(opts.ProjectRoot)
	receiptPath := TrialReceiptPath(opts.ArtifactsRoot, name, staged.TreeHash)
	receipt, err := LoadReceipt(receiptPath)
	if err != nil {
		return nil, err
	}

	acceptedWith := ""
	if opts.Force {
		acceptedWith = "force"
	} else {
		if receipt == nil {
			return nil, fmt.Errorf("no trial receipt for the staged tree (%s) — run `kitsoki kit trial %s` first", receiptPath, name)
		}
		if receipt.To.TreeHash != staged.TreeHash {
			return nil, fmt.Errorf("trial receipt pins tree %s but %s is staged — re-run `kitsoki kit trial %s`", receipt.To.TreeHash, staged.TreeHash, name)
		}
		switch receipt.Result {
		case ResultBlocked:
			return nil, fmt.Errorf("latest trial is blocked (open error items in the worklist) — address them and re-run `kitsoki kit trial %s`", name)
		case ResultPartial:
			if !opts.AllowPartial {
				return nil, fmt.Errorf("latest trial is partial (pending approvals or warnings) — pass --allow-partial to accept anyway (recorded in the receipt)")
			}
			acceptedWith = "partial"
		}
		current := SourceDigests(opts.ProjectRoot, instances, staged.TreeHash)
		for k, v := range receipt.SourceDigests {
			if current[k] != v {
				return nil, fmt.Errorf("source %q changed since the trial (digest drift) — re-run `kitsoki kit trial %s`", k, name)
			}
		}
		worklistPath := filepath.Join(opts.ProjectRoot, receipt.Worklist)
		if receipt.Worklist != "" && FileDigest(worklistPath) != receipt.WorklistDigest {
			return nil, fmt.Errorf("the worklist changed since the trial — re-run `kitsoki kit trial %s` so waivers are re-audited", name)
		}
	}

	// (a) durable acceptance receipt, (b) lock promotion, (c) staging
	// cleanup — in that order, so every crash window is detectable
	// (staged==accepted) and recoverable.
	acceptReceipt := &Receipt{
		Schema:        ReceiptSchema,
		Kit:           name,
		Event:         EventAccept,
		Result:        ResultAccepted,
		From:          staged.From,
		To:            staged.Snapshot(),
		SourceDigests: SourceDigests(opts.ProjectRoot, instances, staged.TreeHash),
		GeneratedAt:   opts.Now().UTC().Format(time.RFC3339),
		AcceptedWith:  acceptedWith,
	}
	if receipt != nil {
		acceptReceipt.Gates = receipt.Gates
		acceptReceipt.Spend = receipt.Spend
		acceptReceipt.LedgerRefs = receipt.LedgerRefs
		acceptReceipt.Worklist = receipt.Worklist
		acceptReceipt.WorklistDigest = receipt.WorklistDigest
	}
	acceptPath := AcceptReceiptPath(opts.ProjectRoot, name, staged.Version)
	if err := WriteReceipt(acceptPath, acceptReceipt); err != nil {
		return nil, err
	}

	constraint := ""
	if locked != nil {
		constraint = locked.Constraint
	}
	lf.Kits[name] = &kitlock.Entry{
		Source:     staged.Source,
		Version:    staged.Version,
		Commit:     staged.Commit,
		TreeHash:   staged.TreeHash,
		Constraint: constraint,
	}
	if err := kitlock.Save(lockPath, lf); err != nil {
		return nil, err
	}
	if err := kitstage.Remove(opts.ProjectRoot, name); err != nil {
		return nil, err
	}

	return &AcceptOutcome{
		Staged:       staged,
		LockPath:     lockPath,
		ReceiptPath:  acceptPath,
		AcceptedWith: acceptedWith,
	}, nil
}
