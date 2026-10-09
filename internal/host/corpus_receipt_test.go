package host_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"kitsoki/internal/corpusreceipt"
	"kitsoki/internal/host"
)

func TestCorpusReceiptHandlerFailsClosedAndUsesInjectedRegistry(t *testing.T) {
	args := map[string]any{"receipt": receiptArgs("calibration", "case-1")}
	result, err := host.CorpusReceiptHandler(nil)(context.Background(), args)
	if err != nil || !strings.Contains(result.Error, "registry is not configured") {
		t.Fatalf("nil registry = %#v, %v", result, err)
	}
	registry := corpusreceipt.Registry{Store: &corpusreceipt.MemoryStore{}}
	result, err = host.CorpusReceiptHandler(registry)(context.Background(), args)
	if err != nil || result.Error != "" || result.Data["receipt"] == nil {
		t.Fatalf("configured registry = %#v, %v", result, err)
	}
}

func receiptArgs(role, id string) map[string]any {
	digest := sha256.Sum256([]byte(`{"command":["test"]}`))
	return map[string]any{"kind": "corpus-receipt.v1", "selection_id": role + "-a", "corpus_role": role, "source_manifest_ref": "fixtures/corpus.yaml", "candidate_count": 1,
		"candidates": []any{map[string]any{"kind": "corpus-case.v1", "id": id, "repo": "example/repo", "source": "arena", "baseline_ref": "base", "fix_ref": "fix", "oracle": map[string]any{"command": []any{"test"}}, "provenance": map[string]any{"adapter": "fixture", "locator": id, "source_digest": "sha256:test"}}},
		"proofs":     []any{map[string]any{"candidate_id": id, "oracle_sha256": hex.EncodeToString(digest[:]), "environment_fingerprint": "fixture", "baseline": map[string]any{"command": []any{"test"}, "exit_code": 1, "environment_fingerprint": "fixture"}, "fix": map[string]any{"command": []any{"test"}, "exit_code": 0, "environment_fingerprint": "fixture"}}}}
}
