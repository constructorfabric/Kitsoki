package corpusreceipt_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"kitsoki/internal/corpusproof"
	"kitsoki/internal/corpusreceipt"
)

func TestRegistryRejectsCrossCohortOverlapBeforeSave(t *testing.T) {
	store := &corpusreceipt.MemoryStore{}
	registry := corpusreceipt.Registry{Store: store}
	if err := registry.Freeze(context.Background(), receipt("calibration-a", corpusreceipt.RoleCalibration, "case-1")); err != nil {
		t.Fatal(err)
	}
	err := registry.Freeze(context.Background(), receipt("heldout-a", corpusreceipt.RoleHeldout, "case-1"))
	if err == nil || !strings.Contains(err.Error(), "already belongs to calibration") {
		t.Fatalf("overlap error = %v", err)
	}
	got, err := registry.List(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("stored receipts = %d, %v", len(got), err)
	}
}

func TestRegistryRejectsInvalidProofAndUnconfiguredStore(t *testing.T) {
	err := (corpusreceipt.Registry{}).Freeze(context.Background(), receipt("x", corpusreceipt.RoleCalibration, "case-1"))
	if !errors.Is(err, corpusreceipt.ErrStoreUnconfigured) {
		t.Fatalf("unconfigured error = %v", err)
	}
	bad := receipt("bad", corpusreceipt.RoleCalibration, "case-2")
	bad.Proofs[0].Baseline.ExitCode = 0
	err = (corpusreceipt.Registry{Store: &corpusreceipt.MemoryStore{}}).Freeze(context.Background(), bad)
	if err == nil || !strings.Contains(err.Error(), "RED/GREEN") {
		t.Fatalf("invalid proof error = %v", err)
	}
}

func receipt(selection, role, id string) corpusreceipt.Receipt {
	return corpusreceipt.Receipt{
		Kind: corpusreceipt.KindV1, SelectionID: selection, CorpusRole: role, SourceManifestRef: "fixtures/corpus.yaml", CandidateCount: 1,
		Candidates: []corpusproof.ProofInput{{Kind: corpusproof.CorpusCaseV1, ID: id, Repo: "example/repo", Source: "arena", BaselineRef: "base", FixRef: "fix", Oracle: json.RawMessage(`{"command":["test"]}`), Provenance: corpusproof.Provenance{Adapter: "fixture", Locator: id, SourceDigest: "sha256:test"}}},
		Proofs:     []corpusproof.Proof{{CandidateID: id, OracleSHA256: oracleDigest(), EnvironmentFingerprint: "fixture", Baseline: corpusproof.RunEvidence{Command: []string{"test"}, ExitCode: 1, EnvironmentFingerprint: "fixture"}, Fix: corpusproof.RunEvidence{Command: []string{"test"}, ExitCode: 0, EnvironmentFingerprint: "fixture"}}},
	}
}

func oracleDigest() string {
	sum := sha256.Sum256([]byte(`{"command":["test"]}`))
	return hex.EncodeToString(sum[:])
}
