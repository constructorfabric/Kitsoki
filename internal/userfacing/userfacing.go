// Package userfacing turns a wrapped Go error into a short, human-readable
// string safe to show a user on any surface (TUI transcript, web error banner).
//
// Why: kitsoki surfaces rendered raw orchestrator/host errors verbatim —
// `error: orchestrator: harness.RunTurn: harness/live: messages.new: POST
// "https://…": 400 …`, or temp paths and `write har.json: %w` fragments. Those
// leak internals, expose filesystem paths, and read as broken. This package is
// the single seam both the TUI (AppendError sites) and the web RPC layer
// (serverErr) route through, so the friendly string is produced one way
// everywhere while the full chain still goes to logs / structured RPC data.
package userfacing

import (
	"path/filepath"
	"regexp"
	"strings"
)

// wrapperPrefixRe matches leading internal Go wrapper prefixes such as
// "orchestrator: ", "SubmitDirect: ", "machine.Turn: " — a dotted identifier
// followed by ": ", repeated one or more times at the start of the string.
var wrapperPrefixRe = regexp.MustCompile(`^(?:[A-Za-z][A-Za-z0-9]*(?:\.[A-Za-z][A-Za-z0-9]*)*: )+`)

// absPathRe matches Unix absolute paths (a slash followed by non-whitespace,
// non-colon characters — enough to capture /path/to/file without consuming the
// ": " separator that follows in most error strings).
var absPathRe = regexp.MustCompile(`/[^\s:,;]+`)

// Error returns a short, user-safe rendering of err: it drops internal wrapper
// prefixes, absolute filesystem paths, and Go formatting artifacts while
// preserving the actionable leaf cause. Returns "" for a nil error.
func Error(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()

	// Strip leading internal wrapper prefixes (e.g. "orchestrator: SubmitDirect: machine.Turn: ").
	msg = wrapperPrefixRe.ReplaceAllString(msg, "")

	// Replace absolute paths with just the base filename so no filesystem
	// internals leak to the user.
	msg = absPathRe.ReplaceAllStringFunc(msg, func(p string) string {
		return filepath.Base(p)
	})

	return strings.TrimSpace(msg)
}
