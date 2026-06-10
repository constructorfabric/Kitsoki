// Package inbound is the read side of a kitsoki session's external surface — the
// poll→intent bridge that lets a Jira ticket or Bitbucket PR thread DRIVE a
// session, not just receive its artifacts.
//
// It is the deliberate counterpart to package transport, which is output-only by
// design ("No inbound polling or webhook receiver. A Transport only posts" —
// internal/transport/doc.go). Rather than grow a read path inside every
// Transport, the inbound bridge is a distinct, opt-in component:
//
//	external thread ──Source.Poll──▶ [BotMarker filter] ──▶ [author filter]
//	    ──Classifier.Classify──▶ (intent, slots) ──Driver.SubmitIntent──▶ turn
//
// The pieces are wired by dependency injection so the package never imports the
// orchestrator or the store:
//
//   - Source       — reads new replies from one (transport, thread). Concrete
//     Jira / Bitbucket implementations (REST clients) plug in here; the package
//     ships only the interface and a fake for tests.
//   - Classifier   — turns a free-text reply into a structured (intent, slots).
//     The default [PrefixClassifier] is deterministic (no LLM): it recognises
//     `continue` / `refine: <text>` / `restart_from <state>` / `jump_to <state>`.
//   - Driver       — advances one turn. The cmd layer adapts the persisted
//     orchestrator (wrapped in the per-session writer lock) to this interface,
//     so concurrent browser + bridge replies serialise on the same session.
//
// Determinism: the default classifier is a pure prefix parse, so a bridge turn
// is reproducible and incurs no model cost. An oracle-backed classifier is a
// future opt-in (proposal open question 3); when one is used, its classification
// is an interpretive decision and is recorded as such by the Driver.
//
// See docs/architecture/transports.md for the drive-vs-transport model.
package inbound
