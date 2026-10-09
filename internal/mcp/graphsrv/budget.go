package graphsrv

import "fmt"

// truncationMarker is appended in-band whenever a payload is cut down to
// fit its byte budget. Per plan §3.3 every truncation must be marked
// in-band — never a silent drop, never a sidecar.
const truncationMarker = "…[truncated]"

// TruncateString caps s at capBytes, cutting on a rune boundary and
// appending truncationMarker, and reports whether truncation happened. A
// non-positive capBytes is treated as "no cap" (returns s unmodified).
func TruncateString(s string, capBytes int) (out string, truncated bool) {
	if capBytes <= 0 || len(s) <= capBytes {
		return s, false
	}
	markerLen := len(truncationMarker)
	keep := capBytes - markerLen
	if keep < 0 {
		keep = 0
	}
	// Cut on a rune boundary so we never split a multi-byte UTF-8 sequence.
	b := []byte(s)[:keep]
	for len(b) > 0 && !isRuneStart(b[len(b)-1]) {
		b = b[:len(b)-1]
	}
	return string(b) + truncationMarker, true
}

// isRuneStart reports whether byte c is not a UTF-8 continuation byte
// (10xxxxxx).
func isRuneStart(c byte) bool {
	return c&0xC0 != 0x80
}

// TruncateSlice caps a []any to at most limit elements, returning the
// truncated slice and whether truncation happened. Used for row/issue
// lists that must be marked in-band (e.g. via a `truncated` response
// field) rather than silently short-counted.
func TruncateSlice[T any](items []T, limit int) (out []T, truncated bool) {
	if limit <= 0 || len(items) <= limit {
		return items, false
	}
	return items[:limit], true
}

// BudgetNote formats a short in-band note describing a truncation, for
// embedding in a response's `truncated_note` (or similar) field alongside a
// boolean `truncated` flag.
func BudgetNote(kind string, kept, total, capBytes int) string {
	return fmt.Sprintf("%s truncated to %d/%d items to stay within the %d-byte response budget", kind, kept, total, capBytes)
}

// Per-tool byte budgets from plan §3.3. Kept as named constants so later
// steps wire the same numbers the plan specifies, rather than re-deriving
// them per handler.
const (
	BudgetGraphOpen      = 2 * 1024  // graph.open: ≤2KB at ~100 nodes
	BudgetGraphGetField  = 2 * 1024  // graph.get: per-field cap
	BudgetGraphGetSingle = 32 * 1024 // graph.get: single-field refetch cap
	BudgetGraphGetTotal  = 24 * 1024 // graph.get: overall per-call budget
	BudgetGraphFindPage  = 8 * 1024  // graph.find: per-page budget
	BudgetGraphNeighbors = 10 * 1024 // graph.neighbors budget

	// P4 write-tool budgets: these responses are small (a changeset id,
	// status, a handful of guard-fills/changed-files) — 8KB matches
	// BudgetGraphFindPage's "generous cap for a page-shaped response"
	// convention rather than re-deriving a tighter number per tool.
	BudgetGraphPropose   = 8 * 1024 // graph.propose
	BudgetGraphChangeset = 8 * 1024 // graph.changeset
	BudgetGraphWithdraw  = 8 * 1024 // graph.withdraw
	BudgetGraphApply     = 8 * 1024 // graph.apply
	BudgetGraphAuthorize = 8 * 1024 // graph.authorize

	// BudgetGraphHistory: graph.history's merged changeset+git timeline
	// page — plan §3.5 ("≤4KB/page").
	BudgetGraphHistory = 4 * 1024
)
