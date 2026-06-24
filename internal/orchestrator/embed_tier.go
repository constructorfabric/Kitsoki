package orchestrator

import (
	"context"
	"log/slog"
	"sync"

	"kitsoki/internal/embed"
	"kitsoki/internal/semroute"
)

// EmbedTierConfig holds routing.embedding.* config loaded from app YAML.
type EmbedTierConfig struct {
	Enabled      bool
	Model        string  // default "nomic-embed-text-v1.5"
	Dim          int     // Matryoshka truncation for nomic (256 default)
	Endpoint     string  // optional: talk to an already-running server
	ConfidentBar float64 // top-1 cosine >= this → confident match
	Margin       float64 // top1 - top2 must exceed this
}

// DefaultEmbedTierConfig returns the default config (disabled, nomic@256,
// confident_bar=0.82, margin=0.08 — placeholder values; calibration updates them).
func DefaultEmbedTierConfig() EmbedTierConfig {
	return EmbedTierConfig{
		Enabled:      false,
		Model:        "nomic-embed-text-v1.5",
		Dim:          256,
		ConfidentBar: 0.82,
		Margin:       0.08,
	}
}

// IntentSpec is the subset of an intent the embed tier needs.
type IntentSpec struct {
	Name string
	// Future: Examples []string; Synonyms []string
}

// EmbedTier runs the embedding routing tier against a set of allowed intents.
// It holds a pre-built intent vector cache (all intents embedded at startup)
// and embeds the query on each turn.
type EmbedTier struct {
	cfg      EmbedTierConfig
	embedder embed.Embedder // inject via constructor; fake in tests

	// intentVecs caches (intentID → normalized vector) built lazily on first call.
	mu         sync.RWMutex
	intentVecs map[string][]float32
}

// NewEmbedTier constructs an EmbedTier. embedder may be nil when cfg.Enabled is
// false (the tier is a no-op in that case).
func NewEmbedTier(cfg EmbedTierConfig, embedder embed.Embedder) *EmbedTier {
	return &EmbedTier{
		cfg:        cfg,
		embedder:   embedder,
		intentVecs: make(map[string][]float32),
	}
}

// Match runs the embedding tier against the given allowed intents and input.
// Returns (verdict, true, nil) on a confident hit.
// Returns (tieVerdict, true, nil) on a tie (top1-top2 < margin but top1 >= bar).
// Returns (zero, false, nil) on a miss or when disabled.
// An error is returned only for non-network failures; sidecar errors return
// (zero, false, nil) (same fail-open contract as the LLM tier).
func (t *EmbedTier) Match(ctx context.Context, intents []IntentSpec, input string) (semroute.Verdict, bool, error) {
	if !t.cfg.Enabled || t.embedder == nil || len(intents) == 0 {
		return semroute.Verdict{}, false, nil
	}

	// Build (or refresh) the intent vector cache for the current allowed set.
	vecs, err := t.ensureIntentVecs(ctx, intents)
	if err != nil {
		// Fail open: embedding errors should not abort the turn.
		slog.Debug("embed tier: intent embed failed", "err", err)
		return semroute.Verdict{}, false, nil
	}

	// Embed the query.
	queryVecs, err := t.embedder.Embed(ctx, []string{input}, embed.Query)
	if err != nil {
		slog.Debug("embed tier: query embed failed", "err", err)
		return semroute.Verdict{}, false, nil
	}
	queryVec := queryVecs[0]

	// Build an index from the cached intent vectors.
	entries := make([]embed.Entry, 0, len(intents))
	for _, spec := range intents {
		vec, ok := vecs[spec.Name]
		if !ok {
			continue
		}
		entries = append(entries, embed.Entry{
			ID:  spec.Name,
			Vec: vec,
		})
	}
	if len(entries) == 0 {
		return semroute.Verdict{}, false, nil
	}

	idx := embed.NewIndex(entries)
	hits := idx.Rank(queryVec, 2)
	if len(hits) == 0 {
		return semroute.Verdict{}, false, nil
	}

	top1 := hits[0]
	if float64(top1.Score) < t.cfg.ConfidentBar {
		return semroute.Verdict{}, false, nil
	}

	// Check margin when there is a second candidate.
	if len(hits) >= 2 {
		top2 := hits[1]
		margin := float64(top1.Score) - float64(top2.Score)
		if margin < t.cfg.Margin {
			// Tie: top-1 clears the bar but the margin is too narrow.
			return semroute.Verdict{
				Confidence: semroute.ConfidenceTie,
				Candidates: []semroute.Candidate{
					{Intent: top1.ID, MatchReason: "embed"},
					{Intent: top2.ID, MatchReason: "embed"},
				},
			}, true, nil
		}
	}

	// Confident hit.
	return semroute.Verdict{
		Intent:      top1.ID,
		Confidence:  semroute.ConfidenceEmbedding,
		MatchReason: "embed",
		MatchKind:   "embed",
	}, true, nil
}

// ensureIntentVecs returns a snapshot map of vectors for the given intents,
// embedding any that are not yet in the cache. Uses a three-phase pattern to
// avoid holding the lock during the (potentially slow) network Embed call:
//
//   - Phase 1 (read lock): identify names missing from the cache.
//   - Phase 2 (no lock): call embedder.Embed for missing names.
//   - Phase 3 (write lock): store the new vectors.
//
// Returns a snapshot copy containing only the entries for the requested
// intents — the live internal map is never returned to the caller.
// Two concurrent goroutines may both detect the same cache miss and both call
// Embed for the same name; this is harmless because the vectors are
// deterministic and the last writer wins.
func (t *EmbedTier) ensureIntentVecs(ctx context.Context, intents []IntentSpec) (map[string][]float32, error) {
	// Phase 1 — read lock: find missing names.
	t.mu.RLock()
	var missing []string
	for _, spec := range intents {
		if _, ok := t.intentVecs[spec.Name]; !ok {
			missing = append(missing, spec.Name)
		}
	}
	t.mu.RUnlock()

	// Phase 2 — no lock: embed missing names (may be slow / network).
	if len(missing) > 0 {
		newVecs, err := t.embedder.Embed(ctx, missing, embed.Document)
		if err != nil {
			return nil, err
		}

		// Phase 3 — write lock: store results.
		t.mu.Lock()
		for i, name := range missing {
			t.intentVecs[name] = newVecs[i]
		}
		t.mu.Unlock()
	}

	// Return a snapshot copy containing only the requested intents.
	t.mu.RLock()
	snapshot := make(map[string][]float32, len(intents))
	for _, spec := range intents {
		if vec, ok := t.intentVecs[spec.Name]; ok {
			snapshot[spec.Name] = vec
		}
	}
	t.mu.RUnlock()

	return snapshot, nil
}
