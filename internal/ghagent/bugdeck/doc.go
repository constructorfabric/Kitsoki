// Package bugdeck deterministically turns a filed kitsoki bug report's evidence
// (an rrweb session replay + a scrubbed HAR network trace) into a single
// self-contained, in-browser-viewable slidey deck — with NO LLM and no paid
// network calls.
//
// The github agent (internal/ghagent) calls this when a bug-report issue is
// filed: it downloads the issue's rrweb.json + har.json release assets, hands
// them here as [Evidence], and gets back one standalone HTML file that embeds
// the replay (it PLAYS in the browser) plus a HAR summary table. The agent then
// hosts that file itself and links it from a comment.
//
// Determinism / no-LLM contract:
//   - [BuildSpec] is a pure transform: evidence bytes → a slidey spec + the clip
//     to write beside it. No network, no clock, no model.
//   - The spec uses captions only — NO narration — so rendering never invokes a
//     text-to-speech voice (which would be a paid network call).
//   - Rendering shells slidey's `bundle` command (see [Renderer]) which inlines
//     every asset into one HTML file. The default [SlideyRenderer] is the only
//     part that touches a subprocess; it is injected so tests stub it out.
//
// The two seams (BuildSpec as a pure function, Renderer as an interface) keep the
// whole package unit-testable without node or slidey installed; a single gated
// integration test exercises the real `slidey bundle`.
package bugdeck
