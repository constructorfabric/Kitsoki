package mining

import "sync"

// MapWatermarkStore is the in-memory, map-backed WatermarkStore. It is seeded
// from webconfig.MiningConfig.MinedThrough and, on every Set, invokes an
// optional Persist callback so the runtime can write the advanced ledger back to
// its sibling state file (the proposal's lean — keeps .kitsoki.yaml
// hand-authored). It is safe for concurrent use across the miner's passes.
//
// "mined == false" (the seed trigger) is presence of the slug key, not a
// non-zero value: a slug that has only ever produced a completed pass with no
// new transcripts still records its key so the seed never re-fires.
type MapWatermarkStore struct {
	mu   sync.Mutex
	m    map[string]int64
	seen map[string]struct{}
	// Persist, when non-nil, is called after every successful Set with a snapshot
	// of the full ledger so the caller can durably write it. A Persist error is
	// returned from Set (the watermark advance is still in memory).
	Persist func(map[string]int64) error
}

// NewMapWatermarkStore builds a store seeded from the given ledger (slug →
// newest-mined mtime). A nil seed starts empty. The seed is copied; the caller's
// map is not retained.
func NewMapWatermarkStore(seed map[string]int64) *MapWatermarkStore {
	s := &MapWatermarkStore{
		m:    make(map[string]int64, len(seed)),
		seen: make(map[string]struct{}, len(seed)),
	}
	for k, v := range seed {
		s.m[k] = v
		s.seen[k] = struct{}{}
	}
	return s
}

// Get returns the slug's newest-mined mtime and whether the slug has ever been
// mined (a completed pass recorded it). An unmined slug ⇒ the seed fires.
func (s *MapWatermarkStore) Get(slug string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, seen := s.seen[slug]
	return s.m[slug], seen
}

// Set advances the slug's watermark and marks it mined. It persists via Persist
// when set; the in-memory advance stands even if Persist errors.
func (s *MapWatermarkStore) Set(slug string, mtime int64) error {
	s.mu.Lock()
	s.m[slug] = mtime
	s.seen[slug] = struct{}{}
	var snapshot map[string]int64
	if s.Persist != nil {
		snapshot = make(map[string]int64, len(s.m))
		for k, v := range s.m {
			snapshot[k] = v
		}
	}
	persist := s.Persist
	s.mu.Unlock()
	if persist != nil {
		return persist(snapshot)
	}
	return nil
}

// Snapshot returns a copy of the current ledger for inspection/persistence.
func (s *MapWatermarkStore) Snapshot() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}
