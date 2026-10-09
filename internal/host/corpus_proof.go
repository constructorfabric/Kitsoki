package host

import (
	"context"
	"encoding/json"
	"fmt"

	"kitsoki/internal/corpusproof"
)

// CorpusProver is the narrow proof dependency used by host.corpus.prove.
// Implementations independently establish a candidate's RED baseline and
// GREEN fix; callers must not substitute worker-reported verification flags.
type CorpusProver interface {
	Prove(context.Context, corpusproof.ProofInput) (corpusproof.Proof, error)
}

// CorpusProofHandler adapts a CorpusProver to host.corpus.prove. The dependency
// is explicit so an application/runtime can install its fixture opener and
// oracle runner, while deterministic flows can replace the registered handler.
// A nil prover fails closed instead of treating unproved candidates as valid.
func CorpusProofHandler(prover CorpusProver) Handler {
	return func(ctx context.Context, args map[string]any) (Result, error) {
		if prover == nil {
			return Result{Error: "host.corpus.prove: proof executor is not configured"}, nil
		}
		raw, ok := args["candidate"]
		if !ok || raw == nil {
			return Result{Error: "host.corpus.prove: candidate is required"}, nil
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return Result{Error: fmt.Sprintf("host.corpus.prove: encode candidate: %v", err)}, nil
		}
		var candidate corpusproof.ProofInput
		if err := json.Unmarshal(encoded, &candidate); err != nil {
			return Result{Error: fmt.Sprintf("host.corpus.prove: decode candidate: %v", err)}, nil
		}
		proof, err := prover.Prove(ctx, candidate)
		if err != nil {
			return Result{Error: "host.corpus.prove: " + err.Error()}, nil
		}
		if proof.CandidateID != candidate.ID {
			return Result{Error: "host.corpus.prove: proof candidate id does not match input candidate"}, nil
		}
		data, err := proofData(proof)
		if err != nil {
			return Result{}, fmt.Errorf("host.corpus.prove: encode proof: %w", err)
		}
		return Result{Data: map[string]any{"proof": data}}, nil
	}
}

func proofData(proof corpusproof.Proof) (map[string]any, error) {
	encoded, err := json.Marshal(proof)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(encoded, &data); err != nil {
		return nil, err
	}
	return data, nil
}
