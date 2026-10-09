package kittrial

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"kitsoki/internal/kitstage"
)

// ReceiptSchema versions the upgrade receipt document, following the
// journey-release-receipt precedent: a result plus the source digests that
// make staleness self-evident — `kit accept` recomputes them and fails
// closed on any drift.
const ReceiptSchema = "kitsoki/kit-upgrade-receipt/v1"

// Receipt events.
const (
	EventTrial  = "trial"
	EventAccept = "accept"
)

// Trial results.
const (
	ResultReady    = "ready"
	ResultPartial  = "partial"
	ResultBlocked  = "blocked"
	ResultAccepted = "accepted"
)

// GateSummary is the receipt-embedded per-gate outcome.
type GateSummary struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Spend    SpendAudit `json:"spend"`
	Fixtures int        `json:"fixtures,omitempty"`
	Checks   int        `json:"checks,omitempty"`
	// Baseline-task accounting (baseline_live gate only).
	SkippedLedger int `json:"skipped_ledger,omitempty"`
	ValidatedNow  int `json:"validated_now,omitempty"`
	PendingCases  int `json:"pending_cases,omitempty"`
	FailedCases   int `json:"failed_cases,omitempty"`
	// Drift accounting (frozen_replay gate only).
	DriftBenign     int `json:"drift_benign,omitempty"`
	DriftViolations int `json:"drift_violations,omitempty"`
	StaleBaseline   int `json:"stale_baseline_legs,omitempty"`
}

// Receipt is the persisted trial/accept record.
type Receipt struct {
	Schema string            `json:"schema"`
	Kit    string            `json:"kit"`
	Event  string            `json:"event"`
	Result string            `json:"result"`
	From   kitstage.Snapshot `json:"from"`
	To     kitstage.Snapshot `json:"to"`
	Gates  []GateSummary     `json:"gates"`
	Spend  SpendAudit        `json:"spend"`
	// LedgerRefs cites the validation-ledger entries this trial consulted
	// or created (case@tree12).
	LedgerRefs []string `json:"ledger_refs,omitempty"`
	// Worklist is the project-relative worklist path; WorklistDigest pins
	// its content (an operator waiver changes the digest — audited).
	Worklist       string `json:"worklist,omitempty"`
	WorklistDigest string `json:"worklist_digest,omitempty"`
	// SourceDigests pin every input that must be unchanged at accept time.
	SourceDigests map[string]string `json:"source_digests"`
	GeneratedAt   string            `json:"generated_at"`
	// AcceptedWith records a non-ready accept (`--allow-partial`) — the
	// audit trail, never a silent bypass.
	AcceptedWith string `json:"accepted_with,omitempty"`
}

// TrialReceiptPath is the digest-keyed trial receipt location: re-trials of
// the same staged tree overwrite the same path (idempotent re-trials).
func TrialReceiptPath(artifactsRoot, kit, toTreeHash string) string {
	return filepath.Join(artifactsRoot, kit, shortHash(toTreeHash), "receipt.json")
}

// AcceptReceiptPath is the durable, committed acceptance receipt location.
func AcceptReceiptPath(projectRoot, kit, version string) string {
	name := fmt.Sprintf("%s@%s.json", kit, version)
	return filepath.Join(projectRoot, ".kitsoki", "receipts", "kit-update", name)
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	if h == "" {
		return "none"
	}
	return h
}

// WriteReceipt marshals r to path (pretty JSON, trailing newline).
func WriteReceipt(path string, r *Receipt) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("kittrial: create %q: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("kittrial: marshal receipt: %w", err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return fmt.Errorf("kittrial: write %q: %w", path, err)
	}
	return nil
}

// LoadReceipt reads a receipt; a missing file returns (nil, nil).
func LoadReceipt(path string) (*Receipt, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("kittrial: read receipt %q: %w", path, err)
	}
	r := &Receipt{}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("kittrial: parse receipt %q: %w", path, err)
	}
	return r, nil
}

// FileDigest hashes one file's bytes ("" for a missing file — absence is a
// valid, comparable state).
func FileDigest(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// SourceDigests computes the digest set both trial (stamping) and accept
// (fail-closed recompute) use: the accepted lock, the staged lock, the
// project profile, every instance manifest, and the staged tree hash
// itself.
func SourceDigests(projectRoot string, instanceApps []string, stagedTreeHash string) map[string]string {
	d := map[string]string{
		"kits_lock":        FileDigest(filepath.Join(projectRoot, ".kitsoki", "kits.lock")),
		"kits_staged_lock": FileDigest(kitstage.Path(projectRoot)),
		"profile":          FileDigest(filepath.Join(projectRoot, ".kitsoki", "project-profile.yaml")),
		"staged_tree":      stagedTreeHash,
	}
	for _, p := range instanceApps {
		d["instance:"+filepath.Base(filepath.Dir(p))] = FileDigest(p)
	}
	return d
}
