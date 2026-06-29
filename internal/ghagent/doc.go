// Package ghagent implements the @kitsoki "mention -> dispatch -> run -> ack"
// loop: it scans the GitHub issue/PR inbox for @kitsoki mentions, claims each
// as an idempotent job, routes its label to a story, spawns that story no-LLM
// through the real flow engine, and posts a rolling-status ack comment.
//
// # Why
//
// The dogfood loop wants a single seam where an operator (or a teammate) can
// say "@kitsoki please fix this" on a GitHub issue and have kitsoki actually
// pick it up, run the mapped pipeline, and report back — all exercised by the
// real engine, not a bespoke mock. This package is that seam.
//
// # Boundaries / non-goals (round 1)
//
//   - The serve daemon handles GitHub-App webhook ingress; polling remains as a
//     fallback/diagnostic producer.
//   - All gh/git I/O flows through host seams so tests stay offline and free.
//   - The PR path ships a real pr_status read + status comment, not a full
//     rebase/review-thread PR-autopilot story.
//   - Rolling status comments edit the first ack in place through
//     host.gh.ticket's comment_edit op with bounded retries; edit failures are
//     recorded on the run instead of posting duplicate comments.
//
// # Pieces
//
//   - mention.go  — the @kitsoki mention filter over the ingress producer.
//   - router.go   — label -> story classification + the default route table.
//   - comment.go  — the rolling-status/ack comment substrate over host.gh.ticket.
//   - dispatch.go — the Dispatcher: claim a job + spawn the mapped story.
//
// # Concurrency note
//
// testrunner.RunFlows publishes KITSOKI_APP_DIR as a process global, so
// concurrent story Dispatch of multiple issue mentions in one process can
// cross-contaminate. The serve loop dispatches synchronously today; per-job
// KITSOKI_APP_DIR isolation is required before parallel story workers.
package ghagent
