package corpusreceipt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"kitsoki/internal/corpusproof"
)

const KindV1 = "corpus-receipt.v1"

const (
	RoleCalibration = "calibration"
	RoleHeldout     = "heldout"
)

var (
	ErrStoreUnconfigured = errors.New("corpus receipt store is not configured")
	ErrSelectionExists   = errors.New("corpus receipt selection already exists")
)

// Receipt is the durable, source-neutral Corpus Forge output. Candidates are
// stored with their independent proof evidence so a future evaluator can audit
// the admission boundary without trusting session state.
type Receipt struct {
	Kind              string                   `json:"kind"`
	SelectionID       string                   `json:"selection_id"`
	CorpusRole        string                   `json:"corpus_role"`
	SourceManifestRef string                   `json:"source_manifest_ref"`
	CandidateCount    int                      `json:"candidate_count"`
	Candidates        []corpusproof.ProofInput `json:"candidates"`
	Proofs            []corpusproof.Proof      `json:"proofs"`
}

// Store is the explicit persistence boundary. Implementations must return all
// prior receipts from the same scope so Registry can reject cross-cohort
// overlap before saving a new receipt.
type Store interface {
	List(context.Context) ([]Receipt, error)
	Save(context.Context, Receipt) error
}

// AtomicFreezer is implemented by durable stores that can perform the
// overlap-check and save as one transaction across independent Registries.
// Registry uses it when available; the small Store interface remains useful
// for deterministic in-memory tests and custom embeddings.
type AtomicFreezer interface {
	Freeze(context.Context, Receipt) error
}

// Registry freezes validated receipts through an explicitly configured Store.
// Its useful zero value is intentionally absent.
type Registry struct{ Store Store }

// Freeze validates receipt evidence and rejects candidate reuse between the
// calibration and heldout cohorts before persisting it.
func (r Registry) Freeze(ctx context.Context, receipt Receipt) error {
	if r.Store == nil {
		return ErrStoreUnconfigured
	}
	if err := Validate(receipt); err != nil {
		return err
	}
	if store, ok := r.Store.(AtomicFreezer); ok {
		return store.Freeze(ctx, receipt)
	}
	prior, err := r.Store.List(ctx)
	if err != nil {
		return fmt.Errorf("list corpus receipts: %w", err)
	}
	if err := validatePrior(prior, receipt); err != nil {
		return err
	}
	if err := r.Store.Save(ctx, receipt); err != nil {
		return fmt.Errorf("save corpus receipt: %w", err)
	}
	return nil
}

func validatePrior(prior []Receipt, receipt Receipt) error {
	for _, existing := range prior {
		if existing.SelectionID == receipt.SelectionID {
			return fmt.Errorf("%w: %s", ErrSelectionExists, receipt.SelectionID)
		}
		if existing.CorpusRole != receipt.CorpusRole {
			if id := firstOverlap(existing, receipt); id != "" {
				return fmt.Errorf("candidate %q already belongs to %s receipt %q", id, existing.CorpusRole, existing.SelectionID)
			}
		}
	}
	return nil
}

// List retrieves every receipt visible to this registry's configured scope.
func (r Registry) List(ctx context.Context) ([]Receipt, error) {
	if r.Store == nil {
		return nil, ErrStoreUnconfigured
	}
	return r.Store.List(ctx)
}

// Validate accepts only complete corpus-receipt.v1 data with a one-to-one,
// independently observed proof for every canonical candidate.
func Validate(receipt Receipt) error {
	if receipt.Kind != KindV1 {
		return fmt.Errorf("kind must be %s", KindV1)
	}
	if strings.TrimSpace(receipt.SelectionID) == "" || strings.TrimSpace(receipt.SourceManifestRef) == "" {
		return errors.New("selection_id and source_manifest_ref are required")
	}
	if receipt.CorpusRole != RoleCalibration && receipt.CorpusRole != RoleHeldout {
		return fmt.Errorf("corpus_role must be %q or %q", RoleCalibration, RoleHeldout)
	}
	if receipt.CandidateCount != len(receipt.Candidates) || len(receipt.Candidates) == 0 || len(receipt.Proofs) != len(receipt.Candidates) {
		return errors.New("candidate_count, candidates, and proofs must be complete and one-to-one")
	}
	proofs := make(map[string]corpusproof.Proof, len(receipt.Proofs))
	for _, proof := range receipt.Proofs {
		if strings.TrimSpace(proof.CandidateID) == "" || strings.TrimSpace(proof.OracleSHA256) == "" || strings.TrimSpace(proof.EnvironmentFingerprint) == "" || len(proof.Baseline.Command) == 0 || len(proof.Fix.Command) == 0 || proof.Baseline.ExitCode == 0 || proof.Fix.ExitCode != 0 || proof.Baseline.EnvironmentFingerprint != proof.EnvironmentFingerprint || proof.Fix.EnvironmentFingerprint != proof.EnvironmentFingerprint {
			return fmt.Errorf("proof for candidate %q is incomplete or not RED/GREEN evidence", proof.CandidateID)
		}
		if _, duplicate := proofs[proof.CandidateID]; duplicate {
			return fmt.Errorf("duplicate proof for candidate %q", proof.CandidateID)
		}
		proofs[proof.CandidateID] = proof
	}
	seen := make(map[string]bool, len(receipt.Candidates))
	for _, candidate := range receipt.Candidates {
		if err := validateCandidate(candidate); err != nil {
			return err
		}
		if seen[candidate.ID] {
			return fmt.Errorf("duplicate candidate id %q", candidate.ID)
		}
		seen[candidate.ID] = true
		proof, ok := proofs[candidate.ID]
		if !ok {
			return fmt.Errorf("candidate %q has no matching proof", candidate.ID)
		}
		canonical, err := json.Marshal(json.RawMessage(candidate.Oracle))
		if err != nil {
			return fmt.Errorf("candidate %q has invalid oracle: %w", candidate.ID, err)
		}
		digest := sha256.Sum256(canonical)
		if proof.OracleSHA256 != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("proof for candidate %q does not match candidate oracle", candidate.ID)
		}
	}
	return nil
}

func validateCandidate(candidate corpusproof.ProofInput) error {
	if candidate.Kind != corpusproof.CorpusCaseV1 {
		return fmt.Errorf("candidate must be a %s", corpusproof.CorpusCaseV1)
	}
	for field, value := range map[string]string{
		"id": candidate.ID, "repo": candidate.Repo, "source": candidate.Source,
		"baseline_ref": candidate.BaselineRef, "fix_ref": candidate.FixRef,
		"provenance.adapter": candidate.Provenance.Adapter, "provenance.locator": candidate.Provenance.Locator,
		"provenance.source_digest": candidate.Provenance.SourceDigest,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("candidate %s is required", field)
		}
	}
	if len(candidate.Oracle) == 0 || string(candidate.Oracle) == "null" {
		return fmt.Errorf("candidate %q oracle is required", candidate.ID)
	}
	return nil
}

func firstOverlap(a, b Receipt) string {
	ids := make(map[string]bool, len(a.Candidates))
	for _, candidate := range a.Candidates {
		ids[candidate.ID] = true
	}
	var shared []string
	for _, candidate := range b.Candidates {
		if ids[candidate.ID] {
			shared = append(shared, candidate.ID)
		}
	}
	sort.Strings(shared)
	if len(shared) == 0 {
		return ""
	}
	return shared[0]
}

// Decode validates arbitrary host input as a receipt. It keeps the handler's
// JSON boundary explicit and avoids accepting a partially typed map.
func Decode(raw any) (Receipt, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return Receipt{}, fmt.Errorf("encode receipt: %w", err)
	}
	var receipt Receipt
	if err := json.Unmarshal(encoded, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("decode receipt: %w", err)
	}
	return receipt, nil
}
