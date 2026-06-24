package turncache

import "time"

// CachedVerdict is the value the orchestrator stores when an LLM turn
// resolves successfully and retrieves on a subsequent matching signature.
//
// The cache treats SlotsJSON as an opaque blob — callers JSON-encode the
// resolved slot map before [Cache.Put] and decode after [Cache.Get]. This
// keeps the cache package free of any AppDef type dependency.
type CachedVerdict struct {
	Intent       string
	SlotsJSON    string  // JSON-encoded map[string]any; the cache doesn't decode.
	Confidence   float64 // Originating LLM verdict's self-reported confidence.
	SourceModel  string  // e.g. "claude-haiku-4-5"; for diagnostics.
	SourceTurnID string  // Back-reference to the originating turn.

	HitCount        int
	LastHitAt       time.Time
	LastVerifiedAt  time.Time
	RevalidateFails int

	CreatedAt time.Time
}

// Key uniquely identifies a cache row.
//
// App is the app id (e.g. "oregon-trail"); AppHash is the hash of the
// AppDef intent surface for the version of the app the row was written
// under. The pair together is what [Cache.InvalidateOtherHashes] uses to
// purge stale rows belonging to a single app without touching rows from
// other apps that happen to share the same on-disk store.
type Key struct {
	App       string
	AppHash   string
	StatePath string
	Signature string
}

// SynonymKey identifies a single declared synonym pattern for hit-tracking.
//
// One row per (AppHash, Intent, Pattern, Kind). Kind is one of
// "bare", "example", "template", or "enum_value", matching the synonym
// surface documented in docs/architecture/semantic-routing.md. "example"
// covers implicit synonyms derived from Intent.Examples — the author wrote
// them as menu prompts, but the matcher still treats them as synonym
// sources and the routing-inspect views want their hits counted.
type SynonymKey struct {
	AppHash string
	Intent  string
	Pattern string
	Kind    string // "bare" | "example" | "template" | "enum_value"
}

// SynonymStat is a single (key, counters) tuple returned by
// [Cache.SynonymStats]. Returned rows are sorted by HitCount descending so
// inspect views can render the hot patterns first.
type SynonymStat struct {
	SynonymKey
	HitCount  int
	LastHitAt time.Time
}
