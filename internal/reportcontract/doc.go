// Package reportcontract names the shared contract for Kitsoki report
// surfaces. It sits below the meta-mode agents, run-status evidence capture,
// and bug filing code so those layers can agree on report kinds, destination
// names, evidence sidecars, and least-privilege tool profiles without importing
// each other.
//
// Bug reports and meta-improve reports are peers at the durable-report layer:
// both may be backed by the same browser-observed HAR, rrweb replay, console
// state, redacted Kitsoki trace, runtime metadata, and local or remote posting
// sink. They differ in intent. A bug report captures a defect or broken user
// expectation. A meta-improve report captures a continuous-improvement lesson:
// a false start, wasted tool call, prompt/tool/script cleanup, permission
// narrowing, or no-LLM regression to add.
//
// # Evidence bundle
//
// Browser-backed reports use the artifact names in this package:
//
//   - screenshot.png, when a screenshot is supplied
//   - har.json, always attempted through the browser capture or server recorder
//   - rrweb.json, when a browser replay is available
//   - console.json, when console or page-error state is supplied
//   - trace.redacted.jsonl, when the Kitsoki session trace can be resolved
//
// These names are intentionally stable because local markdown reports, GitHub
// issue bodies, private ticket providers, playback demos, and tests all link
// to them.
//
// # Agent permission profiles
//
// The report agents share two small tool profiles. ReadOnlyTools is for
// review-only agents such as story-improver and kitsoki-improver; they inspect
// prompts, story files, and traces but do not mutate files or post reports.
// BugFilerTools adds only Bash(kitsoki bug create*) so story-bug-reporter and
// kitsoki-bug-reporter can perform their one expected side effect after user
// confirmation.
//
// # Destinations
//
// DestinationConfigured means "use the server's configured sink": GitHub when
// a ticket repo is configured, otherwise a local artifact report. DestinationLocal
// forces local .artifacts output. DestinationTicketProvider means "write local
// evidence first, then call the configured ticket_provider/v1 script."
//
// # Worked example
//
// A completed web run clicks "Run improve now" with evidence filing enabled.
// The improve agent runs read-only and returns an introspection report. The web
// UI then calls runstatus.meta.improve.report with destination "configured".
// The server normalizes that destination, writes the same artifact names listed
// above, and either leaves a local .artifacts report, files a GitHub issue, or
// posts through a private ticket provider after preserving the local evidence.
//
// # Non-goals
//
//   - No filing implementation. The package names the contract; callers still
//     own markdown creation, GitHub upload, provider invocation, and privacy
//     checks so each surface can keep its existing dependencies.
//   - No policy decision about whether a finding is a bug or an improvement.
//     Agents and users decide that from context.
//   - No artifact redaction. Evidence capture continues to scrub through the
//     bugreport/harscrub path before artifacts are written.
//
// # Reference
//
// User-facing behavior is documented in docs/stories/meta-mode.md and
// docs/workflows/file-a-bug.md. Web evidence capture is documented in
// docs/web/README.md.
package reportcontract
