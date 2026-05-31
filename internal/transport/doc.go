// Package transport defines the [Transport] interface — an output-only
// adapter for posting messages to an external surface — plus a [Registry]
// that dispatches by key and the three built-in drivers ([JiraTransport],
// [BitbucketTransport], [TUITransport]). It sits at the edge of the
// orchestrator: a phase template invokes the `host.transport.post` effect
// with a target transport key, the bridge resolves the [Registry] from the
// context (see [FromContext]), and the registry routes the [Message] to the
// matching driver, which renders and delivers it.
//
// # Architecture
//
// Three pieces compose:
//
//   - [Transport] — the output side of one external surface. A driver knows
//     how to render a [Message] into that surface's native form (Jira wiki,
//     Bitbucket Markdown, an in-process buffer) and deliver it, returning an
//     opaque message ID.
//   - [Registry] — a concurrent-safe map from transport ID to driver. It is
//     the single dispatch point: [Registry.Post] looks up `key.Transport`,
//     fills in defaults, and forwards to the driver's Post.
//   - The drivers — [JiraTransport] and [BitbucketTransport] post over REST;
//     [TUITransport] buffers in-process for the Bubble Tea program to drain.
//
// The drivers deliberately mirror each other's shape (constructor that
// validates config, ID/Post/Close trio, a buildBody helper that prepends the
// bot marker) so the dispatch bridge never special-cases one over another.
//
// # Routing
//
// Two fields steer a Post to the right place:
//
//   - [SessionKey].Transport selects the driver. It must equal some
//     registered [Transport.ID]; otherwise [Registry.Post] returns
//     [ErrTransportNotFound].
//   - The rest of the addressing is per-driver. Jira needs only
//     [SessionKey].Thread (the issue key). Bitbucket needs three PR
//     coordinates — pr_project, pr_slug, pr_id — which do not fit the generic
//     key, so they ride in [Message].Extra; the Bitbucket driver keeps
//     SessionKey.Thread only for orchestrator-side correlation. The TUI
//     driver ignores addressing entirely and just buffers.
//
// Extra is always string-valued so YAML scalars from app.yaml round-trip
// cleanly; drivers coerce as needed.
//
// # Worked example
//
// A phase posting "Patched the off-by-one" to a Jira ticket traces like this:
//
//	registry: { "jira": JiraTransport{base: https://acme.atlassian.net} }
//	call:     Registry.Post(ctx,
//	            SessionKey{Transport:"jira", Thread:"PLTFRM-12345"},
//	            Message{Title:"Fix landed", Body:"Patched the off-by-one"})
//	dispatch: key.Transport "jira" → JiraTransport
//	defaults: BotMarker "" → "[kitsoki]"; Timestamp 0 → now()
//	body:     "[kitsoki] *Fix landed*\n\nPatched the off-by-one"
//	REST:     POST {base}/rest/api/2/issue/PLTFRM-12345/comment
//	            {"body": "<body>"}
//	          200 → {"id":"10001"}
//	return:   ("10001", nil)
//
// The in-process equivalent — dispatch to the TUI driver and drain the buffer
// — is the runnable [ExampleRegistry_Post] / [ExampleTUITransport_Drain].
//
// # Lifecycle
//
// Drivers are constructed and registered once at machine load: build each
// driver from app.yaml config, then [Registry.Register] it under its ID
// (Register panics on a nil driver, an empty ID, or a duplicate — these are
// init-time programming errors, not runtime conditions). After load the
// registry is read-only on the hot path: [Registry.Post], [Registry.Get] and
// [Registry.IDs] take only a read lock and are safe for concurrent use.
// [Registry.Close] closes every driver and is terminal — it nils the map, so
// the registry must not be used afterwards. Individual driver Close is
// idempotent.
//
// # Non-goals
//
//   - No inbound polling or webhook receiver. A Transport only posts;
//     consuming external replies is the orchestrator's job (the loop's poll
//     or a future receiver), because inbound concerns session lifecycle and
//     de-dup that live above this layer.
//   - No fan-out. One Post targets exactly one thread on one surface; a phase
//     that wants to post to two places issues two effects, keeping each
//     delivery individually traceable to a message ID.
//   - No rich document model. The Jira driver stays plain-text wiki (no ADF
//     tree) so one code path works across Cloud and self-hosted; richer
//     formatting would fork the driver per deployment for little gain.
//   - No retry or queueing. A failed Post returns its error to the caller's
//     on_error arc, which owns the policy; baking retries in here would hide
//     failures from the decision record.
//
// # Reference
//
// The user-facing narrative for transports and sessions — the interface, the
// built-in drivers, posting from a state machine, and bot-output filtering —
// is docs/architecture/transports.md. The Markdown-to-Jira-wiki sanitiser the
// Jira driver runs bodies through is internal to this package
// (jira_markdown.go) and mirrors tools/loopy/bugfix/lib/jira.py so the two
// posting surfaces produce identical output.
package transport
