package corpusintake

import (
	"context"
	"fmt"
)

// DiscoveryOptions makes live local-history discovery explicit. The zero value
// cannot inspect a repository, preventing accidental expensive or unsafe scans.
type DiscoveryOptions struct{ AllowLive bool }

// HistorySource supplies reviewable local history records. It is injected so
// callers choose the git implementation and tests never need a real repository.
type HistorySource interface {
	Candidates(context.Context) ([]HistoryRecord, error)
}

// HistoryRecord is the minimum reviewable result of a local history inspector.
type HistoryRecord struct {
	ID, Repo, BaselineRef, FixRef, Ticket, Archetype, Locator, Revision string
	Oracle                                                              any
}

// DiscoverLocal turns inspected local history into proposals only when opt-in
// is explicit. It never invokes git itself.
func DiscoverLocal(ctx context.Context, source HistorySource, options DiscoveryOptions) ([]Candidate, error) {
	if !options.AllowLive {
		return nil, ErrDiscoveryDisabled
	}
	if source == nil {
		return nil, fmt.Errorf("history source: %w", ErrInvalidCandidate)
	}
	records, err := source.Candidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("inspect local history: %w", err)
	}
	out := make([]Candidate, 0, len(records))
	for _, record := range records {
		oracle, err := CanonicalOracle(record.Oracle)
		if err != nil {
			return nil, fmt.Errorf("history record %q: %w", record.ID, err)
		}
		digest, err := Digest(record)
		if err != nil {
			return nil, err
		}
		candidate := Candidate{Kind: KindCorpusCaseV1, ID: record.ID, Repo: record.Repo, Source: SourceLocalGitHistory, BaselineRef: record.BaselineRef, FixRef: record.FixRef, Ticket: record.Ticket, Archetype: record.Archetype, Oracle: oracle, Provenance: Provenance{Adapter: SourceLocalGitHistory, Locator: record.Locator, SourceDigest: digest, Revision: record.Revision}}
		if err := candidate.Validate(); err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	return out, nil
}
