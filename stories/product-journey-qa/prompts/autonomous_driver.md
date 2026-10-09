Drive the product-journey QA autonomous marathon bundle.

Inputs:

- run_dir: `{{ args.run_dir }}`
- project: `{{ args.project }}`
- persona: `{{ args.persona }}`
- scenarios: `{{ args.scenario_scope }}`
- driver mode: `{{ args.autonomous_driver_mode }}`
- live profile: `{{ args.live_profile }}`
- live budget minutes: `{{ args.live_budget_minutes }}`
- ticket repo: `{{ args.ticket_repo }}`
- gh-agent public URL: `{{ args.gh_agent_public_base_url }}`
- control artifact: `{{ args.autonomous_control_path }}`
- report artifact: `{{ args.autonomous_marathon_report_path }}`

Use `.agents/agents/product-journey-qa-driver.md` as the operating contract.
Open or attach a product-journey story session for `stories/product-journey-qa/app.yaml`,
then submit `load run_dir={{ args.run_dir }}` before recording evidence.
After load, inspect `last_result.next_driver_capture_route` before opening a
scenario surface. Use that route's setup entrypoint, resolved open/observe/act
tools, artifact path template, attach command, blocker command, and journal
command. If a route is missing or unusable, return `blocked` and record the
blocker through the product-journey story instead of improvising a setup path.

If driver mode is `replay`, do not open any `harness: "live"` session and do
not retry a replay miss with a live/backend profile. Record the miss as a
blocker through the loaded product-journey story.

If driver mode is `record` or `live`, the supplied live profile is the only
authorization for live work. Open target story sessions with `harness: "live"`
and `profile: "{{ args.live_profile }}"` from the first `session.new` call. If
that profile is empty or unavailable, stop and return `blocked` with a clear
summary; do not infer or use an ambient default backend.

Capture each scenario's proof evidence or record an honest blocker. Record every
attempt through `driver_event`, attach all evidence refs through `attach`, and
record concrete findings through `record` or `blocker`.

Do not submit the final gates from this dispatched driver task. The outer
product-journey story has already queued the autonomous finalizer after this
task returns; that finalizer owns `autonomous_watchdog`, `autonomous_fix`,
review, validation, stats, issue close-out, and gh-agent draining. This keeps
the reliability measures in deterministic story logic instead of agent
judgment.

Do not run `gh` or file/fix issues outside the native product-journey story
path. Return a concise JSON object matching the schema with the driver status,
captured evidence count, issue count, summary, and any trace or blocker
references.
