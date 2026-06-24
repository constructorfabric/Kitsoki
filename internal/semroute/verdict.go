package semroute

// Confidence bands for the semantic-routing tier. Five constants live
// in this file so they're discoverable in one place; Phase 2 only ever
// emits ConfidenceWholeSynonym, ConfidenceTie, and the zero value.
const (
	// ConfidenceExact (1.00) is the deterministic tier's band. semroute
	// never emits it — it exists as a named constant so callers and
	// future tiers don't have to repeat the literal.
	ConfidenceExact = 1.00
	// ConfidenceWholeSynonym (0.90) is the band for a single allowed
	// intent matching via a bare-string synonym or implicit example.
	ConfidenceWholeSynonym = 0.90
	// ConfidenceTemplateAllSlots (0.80) is reserved for Phase 4 (synonym
	// template with every {slot} filled). Unreachable from Phase 2.
	ConfidenceTemplateAllSlots = 0.80
	// ConfidenceTemplateMissingSlot (0.65) is reserved for Phase 4
	// (synonym template with ≥1 named slot unparseable). Unreachable
	// from Phase 2.
	ConfidenceTemplateMissingSlot = 0.65
	// ConfidenceEmbedding (0.82) is the band emitted by the embedding
	// routing tier when the top-1 cosine score clears the confident_bar
	// and the margin over top-2 clears the margin threshold. Must exceed
	// SemanticHighBar (default 0.80) so confident embed hits are direct-routed.
	ConfidenceEmbedding = 0.82
	// ConfidenceTie (0.50) is the band for "two or more allowed
	// intents matched the same input." The caller surfaces a
	// disambiguation card; the verdict carries Candidates.
	ConfidenceTie = 0.50
)

// Verdict is the result of a semantic-routing attempt. The field set is
// shaped so the same struct can carry Phase 2, Phase 3, and Phase 4
// outputs without a shape change.
type Verdict struct {
	// Intent is the resolved intent id, or empty when Confidence is 0
	// (no match) or 0.50 (multi-intent tie — see Candidates).
	Intent string
	// Slots holds slot values extracted by the matcher. Always empty
	// in Phase 2; Phase 4 fills slots from {slot_name} templates.
	Slots map[string]any
	// MissingSlots lists slots the user named but the typed parser
	// could not extract. Always empty in Phase 2.
	MissingSlots []string
	// Confidence is the routing band: 0.90 (synonym hit), 0.50 (tie),
	// or 0 (miss). Phase 4 also emits 0.80 and 0.65.
	Confidence float64
	// MatchReason is a short, machine-readable explanation suitable
	// for both trace events and TUI badges. The Phase 2 shape is
	// "synonym:<source-string>" (e.g. "synonym:wade") for a synonym
	// hit and "example:<source-string>" when the matching source was
	// an implicit-synonym example.
	MatchReason string
	// MatchPattern is the verbatim YAML source string that matched —
	// the bare synonym, the example phrase, or the template (e.g.
	// "wade", "go south", "buy {items} for {total_cost}"). Empty when
	// no match (Confidence == 0) or on a tie (the disambiguation card
	// reads Candidates instead). Consumed by the orchestrator's
	// `RecordSynonymHit` call so the hit-tracking table is keyed by
	// the author's exact string.
	MatchPattern string
	// MatchKind discriminates the source category for hit-tracking
	// and inspect surfaces: "bare" for an authored synonym entry,
	// "example" for an implicit synonym derived from Intent.Examples,
	// "template" for a Phase-4 template match. Empty when no match.
	// Values mirror the [turncache.SynonymKey.Kind] taxonomy so the
	// orchestrator can hand them through without translation.
	MatchKind string
	// Candidates is populated when Confidence == 0.50: every allowed
	// intent that matched the input, listed in stable order so the
	// disambiguation card renders deterministically.
	Candidates []Candidate
}

// Candidate is one entry of a tie verdict. The disambiguation card
// uses Intent to enumerate options and MatchReason to explain *why*
// each candidate was a match in the route-trace overlay.
type Candidate struct {
	Intent      string
	MatchReason string
}
