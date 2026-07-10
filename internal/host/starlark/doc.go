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
// The single argument to main is a struct with inputs, read-only world by
// default, and only the external attributes granted by the run's CapabilitySpec.
// No environment, no clock, no randomness — and only a NARROW opt-in filesystem
// + allow-listed-probe + allow-listed-host surface, never a shell — so a
// recorded run replays byte-for-byte:
//
//	ctx.inputs.<name>            typed inputs resolved from the effect's with.inputs
//	ctx.world.get("key")         read-only snapshot of world; None when absent
//	ctx.http.get(url, headers={})            -> response (with http grant)
//	ctx.http.post(url, body=..., headers={}) -> response (with http grant)
//	ctx.fs.read(path)            read-only, repo-rooted, size-capped -> string (with fs.read grant)
//	ctx.fs.exists(path)          -> bool (with fs.read grant)
//	ctx.fs.glob(pattern)         -> [path] (sorted, repo-relative; with fs.read grant)
//	ctx.fs.write(path, content)  write one repo-rooted, size-capped file -> path (with fs.write grant)
//	ctx.probe(name, args=[])     run an ALLOW-LISTED read-only probe -> {exit, out} (with probe/vcs/github grant)
//	ctx.host.call(name, args={}) invoke an ALLOW-LISTED engine host verb -> dict (with host.verbs grant)
//
// An http response exposes .status (int), .headers (dict), .text() (string),
// and .json() (parsed value). body on post may be a dict (JSON-encoded with an
// application/json content-type) or a string (sent verbatim).
//
// ctx.fs / ctx.probe are the explicit inspection/persistence surface (see
// Inspector): a glue script can assert against the working tree, write one
// bounded file, and call a few curated probes without any way to delete, run a
// shell, or escape the rooted working dir. ctx.probe is a per-deployment
// read-only ALLOW-LIST (gh.issue.list, git.status,
// git.ls_files) — a fixed argv template exec'd directly, NOT a shell. There is
// no ctx.env. A non-zero probe exit is a result the script branches on, not an
// error.
//
// Outputs flow ONLY through main()'s return dict. There is deliberately no
// ctx.world.set — a Starlark effect cannot mutate world out-of-band; everything
// it produces is named, typed, and visible to bind:.
//
// ctx.host.call is the narrow, allow-listed boundary onto the ENGINE's own
// host.Registry (see HostCaller/WithHost in host_proxy.go). The built-in verb
// vocabulary is fixed and tiny, and each run grants a subset through
// CapabilitySpec.Host.Verbs: a name not granted is rejected before the
// underlying Invoke is ever called.
//
// # I/O boundary and record/replay
//
// All network access goes through the HTTPClient interface (see http.go), and
// all filesystem/probe access through the Inspector interface (see inspect.go).
// In production the host adapter injects a recording client backed by net/http
// only when http is granted and http.cassette_required is not set, and a
// working-dir-rooted inspector only when fs or probe-like capabilities are
// granted. In flow tests the testrunner injects a replay client backed by a
// cassette and a ReplayInspector backed by an inspect cassette, so no real
// network/process call is made and the run is deterministic. Each is supplied
// via WithHTTP / WithInspector on the context; HTTPFromContext /
// InspectorFromContext resolve them (both defaulting to a refuse-all
// implementation, so a script that does I/O without an injected client fails
// loudly rather than escaping the sandbox).
//
// Each exchange is recorded as a summary {method, url, status}. The summaries
// — never full request/response bodies — are surfaced for the trace under the
// reserved output key documented on Run. Full bodies live only in cassettes.
//
// # Non-goals
//
//   - No general-purpose plugin host: the ctx surface is fixed in this package,
//     not extensible per-story.
//   - No mutable world; no side effects beyond HTTP and explicit fs/probe.
//   - No general ctx.run shell: ctx.probe is a fixed read-only allow-list, not
//     arbitrary command execution; ctx.fs has no delete/chmod/rename.
//   - No nondeterministic stdlib (time, random) — only json, math, and
//     decode-only yaml are enabled.
//   - This package never imports internal/host; the host.Handler adapter lives
//     in package host (internal/host/starlark_run.go) to avoid an import cycle.
package starlark
