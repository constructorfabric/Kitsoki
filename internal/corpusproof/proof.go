package corpusproof

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CorpusCaseV1 is the only candidate kind accepted by Executor.
const CorpusCaseV1 = "corpus-case.v1"

// ProofInput is the proof package's wire-compatible view of a corpus-case.v1
// candidate. It intentionally mirrors corpusintake.Candidate at the JSON
// boundary instead of importing it, which keeps intake and proof separately
// testable. It has no verification fields: proof is produced independently by
// Executor.
type ProofInput struct {
	Kind        string          `json:"kind"`
	ID          string          `json:"id"`
	Repo        string          `json:"repo"`
	Source      string          `json:"source"`
	BaselineRef string          `json:"baseline_ref"`
	FixRef      string          `json:"fix_ref"`
	Ticket      string          `json:"ticket"`
	Archetype   string          `json:"archetype"`
	Oracle      json.RawMessage `json:"oracle"`
	Provenance  Provenance      `json:"provenance"`
}

// Provenance preserves the adapter's reviewable source information without
// making proof depend on a particular intake package.
type Provenance struct {
	Adapter      string `json:"adapter"`
	Locator      string `json:"locator"`
	SourceDigest string `json:"source_digest"`
	Revision     string `json:"revision,omitempty"`
}

// Fixture identifies one isolated candidate state for a FixtureOpener.
type Fixture struct {
	CandidateID string
	Repo        string
	Source      string
	Ref         string
	Provenance  Provenance
}

// Workspace is an isolated fixture opened by a FixtureOpener. Fingerprint
// names the materialized environment and must be stable for a reproducible
// proof attempt.
type Workspace struct {
	Path        string
	Fingerprint string
	// Release removes an owned materialized workspace. It is optional for
	// externally managed fixtures; Executor invokes it after each proof leg.
	Release func() error `json:"-"`
}

// FixtureOpener materializes one candidate revision in isolation. Implementers
// commonly bridge capsule and repo-history contracts; Executor never opens a
// repository itself.
type FixtureOpener interface {
	Open(context.Context, Fixture) (Workspace, error)
}

// OracleRunner executes a candidate's explicit oracle inside an already
// isolated workspace. It must return the actual command and environment
// fingerprint used, rather than accepting worker self-report.
type OracleRunner interface {
	Run(context.Context, Workspace, json.RawMessage) (RunEvidence, error)
}

// RunEvidence is durable evidence from one oracle attempt.
type RunEvidence struct {
	Command                []string `json:"command"`
	ExitCode               int      `json:"exit_code"`
	Output                 string   `json:"output,omitempty"`
	EnvironmentFingerprint string   `json:"environment_fingerprint"`
}

// Proof records the independently observed RED/GREEN result that admits a
// candidate. It is safe to serialize as a separate receipt payload.
type Proof struct {
	CandidateID            string      `json:"candidate_id"`
	OracleSHA256           string      `json:"oracle_sha256"`
	Baseline               RunEvidence `json:"baseline"`
	Fix                    RunEvidence `json:"fix"`
	EnvironmentFingerprint string      `json:"environment_fingerprint"`
}

// RejectionKind classifies a deterministic reason a candidate cannot be
// admitted. Callers can use errors.As rather than parsing error text.
type RejectionKind string

const (
	KindMissingOracle    RejectionKind = "missing_oracle"
	KindAlreadyGreen     RejectionKind = "already_green"
	KindNonReproducible  RejectionKind = "non_reproducible_environment"
	KindInvalidCandidate RejectionKind = "invalid_candidate"
)

// Rejection is a typed, reviewable admission refusal.
type Rejection struct {
	Kind   RejectionKind
	Detail string
}

func (r *Rejection) Error() string {
	if r.Detail == "" {
		return string(r.Kind)
	}
	return fmt.Sprintf("%s: %s", r.Kind, r.Detail)
}

// IsRejection reports whether err is a Rejection of kind.
func IsRejection(err error, kind RejectionKind) bool {
	var rejection *Rejection
	return errors.As(err, &rejection) && rejection.Kind == kind
}

// Executor coordinates proof while leaving all side effects dependency
// injected. It has no useful zero value.
type Executor struct {
	Fixtures FixtureOpener
	Runner   OracleRunner
}

// Prove independently establishes baseline RED and fix GREEN for candidate.
// It returns typed rejections for an absent oracle, an already-green baseline,
// and non-reproducible fixture or runner environments.
func (e Executor) Prove(ctx context.Context, candidate ProofInput) (Proof, error) {
	oracle, hash, err := canonicalOracle(candidate)
	if err != nil {
		return Proof{}, err
	}
	if e.Fixtures == nil || e.Runner == nil {
		return Proof{}, &Rejection{Kind: KindInvalidCandidate, Detail: "fixture opener and oracle runner are required"}
	}
	if err := validateCandidate(candidate); err != nil {
		return Proof{}, err
	}

	baselineWorkspace, err := e.Fixtures.Open(ctx, fixtureFor(candidate, candidate.BaselineRef))
	if err != nil {
		return Proof{}, &Rejection{Kind: KindNonReproducible, Detail: "open baseline fixture: " + err.Error()}
	}
	fixWorkspace, err := e.Fixtures.Open(ctx, fixtureFor(candidate, candidate.FixRef))
	if err != nil {
		return Proof{}, &Rejection{Kind: KindNonReproducible, Detail: "open fix fixture: " + err.Error()}
	}
	defer releaseWorkspace(baselineWorkspace)
	defer releaseWorkspace(fixWorkspace)
	if baselineWorkspace.Fingerprint == "" || fixWorkspace.Fingerprint == "" || baselineWorkspace.Fingerprint != fixWorkspace.Fingerprint {
		return Proof{}, &Rejection{Kind: KindNonReproducible, Detail: "fixture environment fingerprints differ or are absent"}
	}

	baseline, err := e.Runner.Run(ctx, baselineWorkspace, oracle)
	if err != nil {
		return Proof{}, &Rejection{Kind: KindNonReproducible, Detail: "run baseline oracle: " + err.Error()}
	}
	fix, err := e.Runner.Run(ctx, fixWorkspace, oracle)
	if err != nil {
		return Proof{}, &Rejection{Kind: KindNonReproducible, Detail: "run fix oracle: " + err.Error()}
	}
	if err := validateEvidence(baseline, fix, baselineWorkspace.Fingerprint); err != nil {
		return Proof{}, err
	}
	if baseline.ExitCode == 0 {
		return Proof{}, &Rejection{Kind: KindAlreadyGreen, Detail: "baseline oracle exited zero"}
	}
	if fix.ExitCode != 0 {
		return Proof{}, &Rejection{Kind: KindNonReproducible, Detail: fmt.Sprintf("fix oracle exited %d", fix.ExitCode)}
	}
	return Proof{CandidateID: candidate.ID, OracleSHA256: hash, Baseline: baseline, Fix: fix, EnvironmentFingerprint: baselineWorkspace.Fingerprint}, nil
}

func releaseWorkspace(workspace Workspace) {
	if workspace.Release != nil {
		_ = workspace.Release()
	}
}

func fixtureFor(c ProofInput, ref string) Fixture {
	return Fixture{CandidateID: c.ID, Repo: c.Repo, Source: c.Source, Ref: ref, Provenance: c.Provenance}
}

func canonicalOracle(c ProofInput) (json.RawMessage, string, error) {
	if len(c.Oracle) == 0 || string(c.Oracle) == "null" {
		return nil, "", &Rejection{Kind: KindMissingOracle, Detail: "candidate.oracle is required"}
	}
	var value any
	if err := json.Unmarshal(c.Oracle, &value); err != nil || value == nil {
		if err != nil {
			return nil, "", &Rejection{Kind: KindMissingOracle, Detail: "candidate.oracle is invalid JSON"}
		}
		return nil, "", &Rejection{Kind: KindMissingOracle, Detail: "candidate.oracle is required"}
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("canonicalize oracle: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(digest[:]), nil
}

func validateCandidate(c ProofInput) error {
	if c.Kind != CorpusCaseV1 {
		return &Rejection{Kind: KindInvalidCandidate, Detail: "kind must be " + CorpusCaseV1}
	}
	if c.Source != "local-git-history" && c.Source != "external-bakeoff" && c.Source != "arena" {
		return &Rejection{Kind: KindInvalidCandidate, Detail: "source must be local-git-history, external-bakeoff, or arena"}
	}
	for field, value := range map[string]string{"id": c.ID, "repo": c.Repo, "source": c.Source, "baseline_ref": c.BaselineRef, "fix_ref": c.FixRef, "provenance.adapter": c.Provenance.Adapter, "provenance.locator": c.Provenance.Locator, "provenance.source_digest": c.Provenance.SourceDigest} {
		if strings.TrimSpace(value) == "" {
			return &Rejection{Kind: KindInvalidCandidate, Detail: field + " is required"}
		}
	}
	return nil
}

func validateEvidence(baseline, fix RunEvidence, expectedFingerprint string) error {
	if len(baseline.Command) == 0 || len(fix.Command) == 0 {
		return &Rejection{Kind: KindNonReproducible, Detail: "oracle runner did not record command evidence"}
	}
	if baseline.EnvironmentFingerprint == "" || fix.EnvironmentFingerprint == "" || baseline.EnvironmentFingerprint != expectedFingerprint || fix.EnvironmentFingerprint != expectedFingerprint {
		return &Rejection{Kind: KindNonReproducible, Detail: "runner environment fingerprints differ from fixture"}
	}
	return nil
}
