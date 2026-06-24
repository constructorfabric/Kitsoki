// Package starlark is the deterministic sandbox behind the host.starlark.run
// capability. It runs a small, author-supplied Starlark script in a tightly
// restricted interpreter and returns a map of named outputs.
//
// Why this exists: kitsoki's value proposition is separating interpretive
// decisions (the LLM agent) from deterministic execution. A great deal of
// "glue" in a story — shaping a payload, calling a plain HTTP API, deriving a
// few fields — is deterministic but too fiddly to express in the YAML effect
// vocabulary and too small to justify a bespoke Go host handler. host.run can
// shell out, but a shell command is opaque to the trace, unsandboxed, and
// non-portable. This package gives authors a real expression language that is
// nonetheless deterministic, introspectable, and replayable.
//
// # The contract
//
// An author writes two files that live beside the story:
//
//	derive.star        — the script; must define main(ctx) -> dict
//	derive.star.yaml   — the sidecar; declares typed inputs and outputs
//
// Run loads the script, validates the effect's inputs against the sidecar,
// evaluates main(ctx), then validates the returned dict against the sidecar's
// declared outputs. The returned outputs become the host.Result.Data the
// orchestrator binds into world per the effect's bind: spec.
//
// # The ctx surface (deliberately narrow)
//
// The single argument to main is a struct with exactly five attributes. No
// environment, no clock, no randomness — and only a NARROW read-only filesystem
// + allow-listed-probe surface, never a shell — so a recorded run replays
// byte-for-byte:
//
//	ctx.inputs.<name>            typed inputs resolved from the effect's with.inputs
//	ctx.world.get("key")         read-only snapshot of world; None when absent
//	ctx.http.get(url, headers={})            -> response
//	ctx.http.post(url, body=..., headers={}) -> response
//	ctx.fs.read(path)            read-only, repo-rooted, size-capped -> string
//	ctx.fs.exists(path)          -> bool
//	ctx.fs.glob(pattern)         -> [path] (sorted, repo-relative)
//	ctx.probe(name, args=[])     run an ALLOW-LISTED read-only probe -> {exit, out}
//
// An http response exposes .status (int), .headers (dict), .text() (string),
// and .json() (parsed value). body on post may be a dict (JSON-encoded with an
// application/json content-type) or a string (sent verbatim).
//
// ctx.fs / ctx.probe are the read-only inspection surface (see Inspector): a
// verify gate can assert against the working tree and a few curated probes
// without any way to write, delete, or escape the rooted working dir. ctx.probe
// is a per-deployment read-only ALLOW-LIST (gh.issue.list, git.status,
// git.ls_files) — a fixed argv template exec'd directly, NOT a shell. There is
// no ctx.env. A non-zero probe exit is a result the script branches on, not an
// error.
//
// Outputs flow ONLY through main()'s return dict. There is deliberately no
// ctx.world.set — a Starlark effect cannot mutate world out-of-band; everything
// it produces is named, typed, and visible to bind:.
//
// # I/O boundary and record/replay
//
// All network access goes through the HTTPClient interface (see http.go), and
// all filesystem/probe access through the Inspector interface (see inspect.go).
// In production the orchestrator injects a recording client backed by net/http
// and a working-dir-rooted inspector; in flow tests the testrunner injects a
// replay client backed by a cassette and a ReplayInspector backed by an inspect
// cassette, so no real network/process call is made and the run is
// deterministic. Each is supplied via WithHTTP / WithInspector on the context;
// HTTPFromContext / InspectorFromContext resolve them (both defaulting to a
// refuse-all implementation, so a script that does I/O without an injected
// client fails loudly rather than escaping the sandbox).
//
// Each exchange is recorded as a summary {method, url, status}. The summaries
// — never full request/response bodies — are surfaced for the trace under the
// reserved output key documented on Run. Full bodies live only in cassettes.
//
// # Non-goals
//
//   - No general-purpose plugin host: the ctx surface is fixed in this package,
//     not extensible per-story.
//   - No mutable world; no side effects beyond HTTP and READ-ONLY fs/probe.
//   - No general ctx.run shell: ctx.probe is a fixed read-only allow-list, not
//     arbitrary command execution; ctx.fs is read-only with no write/delete.
//   - No nondeterministic stdlib (time, random) — only json and math are enabled.
//   - This package never imports internal/host; the host.Handler adapter lives
//     in package host (internal/host/starlark_run.go) to avoid an import cycle.
package starlark
