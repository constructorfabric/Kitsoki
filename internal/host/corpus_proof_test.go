package host_test

import (
	"context"
	"strings"
	"testing"

	"kitsoki/internal/corpusproof"
	"kitsoki/internal/host"
)

type corpusProverStub struct {
	input corpusproof.ProofInput
	proof corpusproof.Proof
	err   error
}

func (s *corpusProverStub) Prove(_ context.Context, input corpusproof.ProofInput) (corpusproof.Proof, error) {
	s.input = input
	return s.proof, s.err
}

func TestCorpusProofHandler_UsesInjectedProver(t *testing.T) {
	stub := &corpusProverStub{proof: corpusproof.Proof{CandidateID: "case-1", OracleSHA256: "abc", EnvironmentFingerprint: "fixture-v1"}}
	result, err := host.CorpusProofHandler(stub)(context.Background(), map[string]any{"candidate": candidateArgs()})
	if err != nil {
		t.Fatalf("handler returned infrastructure error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("handler returned domain error: %s", result.Error)
	}
	if stub.input.Kind != corpusproof.CorpusCaseV1 || stub.input.ID != "case-1" {
		t.Fatalf("injected prover received %+v", stub.input)
	}
	proof, ok := result.Data["proof"].(map[string]any)
	if !ok || proof["candidate_id"] != "case-1" || proof["environment_fingerprint"] != "fixture-v1" {
		t.Fatalf("proof result = %#v", result.Data)
	}
}

func TestCorpusProofHandler_RejectsMissingOrUnavailableProof(t *testing.T) {
	result, err := host.CorpusProofHandler(nil)(context.Background(), map[string]any{"candidate": candidateArgs()})
	if err != nil || result.Error != "host.corpus.prove: proof executor is not configured" {
		t.Fatalf("nil prover result = %#v, %v", result, err)
	}

	stub := &corpusProverStub{err: &corpusproof.Rejection{Kind: corpusproof.KindAlreadyGreen, Detail: "baseline oracle exited zero"}}
	result, err = host.CorpusProofHandler(stub)(context.Background(), map[string]any{"candidate": candidateArgs()})
	if err != nil || !strings.Contains(result.Error, "already_green") {
		t.Fatalf("rejection result = %#v, %v", result, err)
	}
}

func TestCorpusProofHandler_RejectsMismatchedProofIdentity(t *testing.T) {
	stub := &corpusProverStub{proof: corpusproof.Proof{CandidateID: "other"}}
	result, err := host.CorpusProofHandler(stub)(context.Background(), map[string]any{"candidate": candidateArgs()})
	if err != nil || result.Error != "host.corpus.prove: proof candidate id does not match input candidate" {
		t.Fatalf("mismatched proof result = %#v, %v", result, err)
	}
}

func TestCorpusProofHandler_RegisteredFailClosed(t *testing.T) {
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	result, err := reg.Invoke(context.Background(), "host.corpus.prove", map[string]any{"candidate": candidateArgs()})
	if err != nil || result.Error != "host.corpus.prove: proof executor is not configured" {
		t.Fatalf("registered result = %#v, %v", result, err)
	}
}

func candidateArgs() map[string]any {
	return map[string]any{
		"kind":         "corpus-case.v1",
		"id":           "case-1",
		"repo":         "example/repo",
		"source":       "arena",
		"baseline_ref": "base",
		"fix_ref":      "fix",
		"ticket":       "ticket",
		"archetype":    "bugfix",
		"oracle":       map[string]any{"command": []any{"go", "test", "./..."}},
		"provenance": map[string]any{
			"adapter":       "test",
			"locator":       "fixture",
			"source_digest": "sha256:abc",
		},
	}
}
