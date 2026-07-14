# Story-native CI

Capsule CI treats a Kitsoki story as the pipeline. `.kitsoki/ci.yaml` selects
the story, environment, executor, trigger policy, agent budget, cleanup policy,
and typed result contract; the story owns the actual phases and gates.

The important boundary is that `.kitsoki/ci.yaml` is not a shell-step DAG. A
project can compose deterministic checks, schema-bounded review, bounded writer
loops, human checkpoints, and adjudication as normal rooms. The runner only
accepts a `capsule-ci-verdict/v1` that matches the sealed execution envelope.

## Contract

A CI story receives normalized world values from the runner:

- `ci_job_id`
- `ci_pipeline`
- `ci_trigger`
- `ci_source`
- `ci_workspace`
- `ci_environment`
- `ci_policy`

It must emit `world.ci_verdict` with:

```json
{
  "schema": "capsule-ci-verdict/v1",
  "pipeline": "change",
  "outcome": "passed",
  "checks": [
    {
      "id": "deterministic",
      "kind": "deterministic",
      "outcome": "passed",
      "evidence": ["artifact:test-log"]
    }
  ],
  "promotion_eligible": true,
  "source_digest": "sha256:...",
  "story_digest": "sha256:...",
  "environment_digest": "sha256:...",
  "envelope_digest": "sha256:..."
}
```

Promotion eligibility is derived by the runner. A story cannot make a failed,
missing-evidence, digest-mismatched, or parked run promotion eligible by setting
the boolean manually.

## Reference story

`stories/capsule-ci` is the reference composition. It demonstrates:

- `prepare`
- `deterministic_checks`
- `llm_review`
- `refine_change`
- `adjudicate`

The default `run` path invokes `host.capsule_ci.project_checks`. That
deterministic host reads the materialized workspace's checked-in
`.kitsoki/project-profile.yaml`, runs declared `test` and `build` commands,
writes bounded evidence under `.artifacts/capsule-ci/checks/`, and constructs a
digest-bound verdict. It parks with `needs_input` when neither command exists
and fails when a command fails; it never fabricates green evidence.

No-LLM flow fixtures cover pass, fail, park, review pass, and budget exhaustion.
Runtime tests run the same story contract through host, fake remote, and fake
container placements.

The host lane is a trusted local compatibility lane (`network: live`,
`external_write: allow`), not a network sandbox. Tests may inject a contained
fake host capability to compare the same no-egress envelope across placements;
that fixture does not claim production host containment. Use container or a
worker deployment with an independently enforced none/replay network for a
constrained CI policy.

The optional LLM review room is also covered without live model spend:
`llm-review-cassette.yaml` replays the `host.agent.decide` call from a host
cassette, binds the schema-bounded review verdict, and then follows the same
adjudication path as a real review.

Generated project wrappers are tested separately across host, fake remote, and
fake container placements. The same injected command runner proves pass/fail/
no-command behavior without forking real commands, and the runtime rejects any
wrapper verdict whose source, story, environment, or envelope digest differs
from the sealed envelope.

## Agent authority

Reviewer and writer rooms should receive explicit toolboxes. A writer that
modifies code should receive only the Capsule MCP workspace handle for the
leased workspace unless the story deliberately grants more authority. Remote
publish, PR creation, and protected-branch promotion are separate grants and
are not implied by a green CI verdict.

The execution envelope adds a non-bypassable agent policy around every
`host.agent.*` handler after story/test host configuration. Agent calls default
to `deny`. An `allow` pipeline must seal an explicit profile allowlist, positive
`max_cost_usd`, and `on_unavailable` fallback; each call must name an allowed
profile, and cost is checked before dispatch and again after the one-shot run.
The launcher also applies the sealed external-write policy after every
embedding/test host registration, so external host effects cannot be reopened
by a project wrapper under `external_write: deny`.

The no-LLM writer proof uses the Capsule MCP server directly: the writer-visible
tool catalog contains only `capsule.*` tools, mutation goes through
`capsule.fs.write`, local history goes through `capsule.vcs.commit`, and raw
argv is denied unless the immutable startup grant includes `raw_exec`.

## GitHub ingress

GitHub is an adapter, not the CI engine. Pull-request webhook data normalizes
to the standard `ci.Trigger` shape, and the check-run adapter projects the
typed Capsule verdict to a GitHub check conclusion. Automated tests use pure
projection plus a mocked HTTP publisher and do not contact GitHub.

For local/offline adapter testing:

```sh
kitsoki capsule ci github trigger \
  --payload payload.json --pipeline change > trigger.json
kitsoki capsule ci run change --workspace change-1 --trigger trigger.json
kitsoki capsule ci github check --project . --job <job-id> --details-url <run-url>
```

For explicit publication:

```sh
GH_TOKEN=... kitsoki capsule ci github publish-check \
  --project . \
  --job <job-id> \
  --repo owner/repo \
  --details-url <run-url>
```

`check` emits the JSON a GitHub service would publish; `publish-check` owns the
credentialed network write and returns
`capsule-ci-github-check-publication/v1`. Credentials stay outside the story
contract and are not written to traces or run records.

`capsule ci plan|run --trigger <file|->` is the ingress seam shared by adapters.
It accepts the normalized trigger only and rejects a trigger that requests a
different pipeline, so raw webhook payloads never become unbounded story world.

## Failure diagnosis

Every completed or failed Capsule CI run should leave a local run record under
`.capsules/ci/`. Failed runs also persist the diagnostic trace even when no
promotion receipt can be signed. Use:

```sh
kitsoki capsule ci diagnose --job <job-id>
kitsoki capsule ci diagnose --latest --stall-after 2m --json=false
kitsoki capsule ci status --job <job-id> --refresh
```

The command projects the run, terminal error, executor failure kind, trace and
receipt sidecars, last durable stage/activity, open executor span, stall
classification, and copy-ready follow-up commands into
`capsule-ci-run-diagnosis/v1`. This is the first debugging surface for remote
worker stalls, story verdict mismatches, and infra failures; raw traces remain
the deeper evidence layer.

Run `kitsoki capsule ci doctor <pipeline> --workspace <id>` before a live or
remote run. It validates configuration, story closure, environment, workspace,
credential names, executor policy, timeouts, and disk hygiene without uploading
source or invoking the story.

Remote cancellation is worker-confirmed rather than a local status rewrite.
`capsule ci cancel --job <id>` requests cancellation through the selected
durable executor; refresh status until the worker reports terminal `cancelled`.
If it reports `completed` or `failed`, the run remains that outcome.
