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
// KITSOKI_APP_DIR is still a process-global env var (app.Load's env-var
// validator reads it synchronously to resolve `${KITSOKI_APP_DIR}`
// references such as meta_modes[*].cwd), but testrunner now serializes only
// the narrow setenv-then-Load span behind a package mutex
// (internal/testrunner/flows.go's appDirLoadMu / loadAppForRun), not whole
// flow/turn runs. Two jobs' RunStorySession calls dispatched concurrently in
// one process load their app.yaml one at a time — briefly serialized, never
// cross-contaminated — and then run their turns fully concurrently, since
// post-Load prompt/script resolution goes through the per-orchestrator
// def.BaseDir-scoped render.AppRenderer
// (internal/host/prompt_render.go), not the global env var. See
// TestConcurrentDispatch_NoAppDirCrossContamination in dispatch_e2e_test.go.
// The one residual gap: internal/host/starlark_run.go's Starlark-inspector
// root still falls back to the global env var when world.workdir is unset,
// so a per-job worktree (task 2.1) should always seed world.workdir.
//
// # Real dispatch (task 2)
//
// Routes with a registered realdispatch.go plan (stories/bugfix only today —
// see that file's doc comment) run the REAL machine end-to-end instead of the
// beat-fixture stub: a per-job worktree identity is seeded through
// stories/bugfix's own sanctioned workspace_prepared/.worktrees-prefix
// exemption (rooms/idle.yaml Step-0w), which always sets world.workdir,
// closing the starlark_run.go gap above for every real-dispatch job. The
// harness that serves host.agent.*/host.git/host.local calls is selected by
// resolveHarnessMode (dispatch.go): "replay" (a recorded, arg-matched
// cassette — no LLM, CI-safe, the default) or "live" (the real agent
// subprocess — real cost, operator-invoked only via Dispatcher.HarnessMode or
// the KITSOKI_GHAGENT_HARNESS env var, never auto-detected from ambient
// credentials). Routes without a plan yet (stories/dev-story) still run the
// honest beat-fixture stub from tasks 0/1.
package ghagent
