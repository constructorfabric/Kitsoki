package corpusintake

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// KindCorpusCaseV1 identifies the stable source-neutral candidate contract.
	KindCorpusCaseV1 = "corpus-case.v1"
	// SourceArena identifies a candidate imported from an Arena corpus manifest.
	SourceArena = "arena"
	// SourceExternalBakeoff identifies a candidate imported from a bugfix-bakeoff manifest.
	SourceExternalBakeoff = "external-bakeoff"
	// SourceLocalGitHistory identifies a candidate proposed from local git history.
	SourceLocalGitHistory = "local-git-history"
)

// Candidate is a proposed corpus task. It is not admitted until an independent
// proof executor records RED at BaselineRef and GREEN at FixRef.
type Candidate struct {
	Kind        string     `json:"kind" yaml:"kind"`
	ID          string     `json:"id" yaml:"id"`
	Repo        string     `json:"repo" yaml:"repo"`
	Source      string     `json:"source" yaml:"source"`
	BaselineRef string     `json:"baseline_ref" yaml:"baseline_ref"`
	FixRef      string     `json:"fix_ref" yaml:"fix_ref"`
	Ticket      string     `json:"ticket" yaml:"ticket"`
	Archetype   string     `json:"archetype" yaml:"archetype"`
	Oracle      Oracle     `json:"oracle" yaml:"oracle"`
	Provenance  Provenance `json:"provenance" yaml:"provenance"`
}

// Oracle preserves the source's oracle declaration as canonical JSON. The
// proof seam owns interpretation, so adapters never turn it into an execution.
type Oracle json.RawMessage

// Provenance lets a reviewer find and hash-check the immutable source record.
type Provenance struct {
	Adapter      string `json:"adapter" yaml:"adapter"`
	Locator      string `json:"locator" yaml:"locator"`
	SourceDigest string `json:"source_digest" yaml:"source_digest"`
	Revision     string `json:"revision,omitempty" yaml:"revision,omitempty"`
}

// Validate reports malformed candidate data before it reaches a reviewer or
// proof executor. It accepts arbitrary valid JSON for Oracle because oracle
// semantics are intentionally source- and runner-specific.
func (c Candidate) Validate() error {
	if c.Kind != KindCorpusCaseV1 {
		return fmt.Errorf("candidate kind %q: %w", c.Kind, ErrInvalidCandidate)
	}
	for name, value := range map[string]string{"id": c.ID, "repo": c.Repo, "source": c.Source, "baseline_ref": c.BaselineRef, "fix_ref": c.FixRef, "ticket": c.Ticket, "archetype": c.Archetype, "provenance.adapter": c.Provenance.Adapter, "provenance.locator": c.Provenance.Locator, "provenance.source_digest": c.Provenance.SourceDigest} {
		if value == "" {
			return fmt.Errorf("candidate %s: %w", name, ErrInvalidCandidate)
		}
	}
	if !json.Valid(c.Oracle) || string(c.Oracle) == "null" {
		return fmt.Errorf("candidate oracle: %w", ErrInvalidCandidate)
	}
	return nil
}

// CanonicalOracle turns decoded YAML/JSON values into a deterministic JSON oracle.
func CanonicalOracle(v any) (Oracle, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal oracle: %w", err)
	}
	if !json.Valid(b) || string(b) == "null" {
		return nil, fmt.Errorf("oracle: %w", ErrInvalidCandidate)
	}
	return Oracle(b), nil
}

// Digest returns a SHA-256 digest of canonical JSON. It makes provenance
// reviewable without treating an unverified source record as proof.
func Digest(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal provenance: %w", err)
	}
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:]), nil
}

var (
	// ErrInvalidCandidate identifies a structurally incomplete proposal.
	ErrInvalidCandidate = errors.New("invalid corpus candidate")
	// ErrDiscoveryDisabled prevents a caller from accidentally initiating live discovery.
	ErrDiscoveryDisabled = errors.New("live corpus discovery is disabled")
)
