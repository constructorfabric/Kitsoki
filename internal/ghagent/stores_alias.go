package ghagent

import "kitsoki/internal/ghagent/bugdeck"

// The bug-deck stores + id formula live in the leaf bugdeck package so the web
// bug-filer can deposit evidence (keyed by the same DeckID) without importing
// the whole gh-agent. These aliases keep the ghagent.* spellings working for the
// reactor, trigger, and CLI wiring.
type (
	// DeckStore hosts rendered decks; see [bugdeck.DeckStore].
	DeckStore = bugdeck.DeckStore
	// EvidenceStore holds per-issue bug evidence; see [bugdeck.EvidenceStore].
	EvidenceStore = bugdeck.EvidenceStore
)

var (
	NewDeckStore     = bugdeck.NewDeckStore
	NewEvidenceStore = bugdeck.NewEvidenceStore
	// DeckID maps (repo, issue) → the shared deck/evidence id.
	DeckID = bugdeck.DeckID
	// SanitizeDeckID makes an id a safe single path segment.
	SanitizeDeckID = bugdeck.SanitizeDeckID
)
