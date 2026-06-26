# GLM-5.2 quota dogfood: control the provider before it controls the run

Kitsoki's GLM-5.2 dogfood on 2026-06-26 was a deliberately small live run:
one proposal-decomposition task, one synthetic.new-backed profile, one quota
slot, and no competing provider work. The goal was not to prove GLM was the
best model. It was to prove that Kitsoki could run a real provider-backed
pipeline without stampeding into HTTP 429s, then turn the trace into a
repeatable model-task-engineering loop.

The result was useful because it separated two failure modes that are easy to
confuse:

- **Quota behavior worked.** The local quota controller serialized the live call,
  persisted the reservation, released it, and updated future assumptions from
  observed usage. No 429 or backoff was recorded.
- **GLM task performance still failed the bench.** The model completed and
  submitted a valid decomposition, but exceeded wall-clock, output-token, and
  cost budgets.

That distinction is the point of the feature. Without local quota accounting,
a slow or oversized model run often looks like "the provider is flaky." With
the quota state and trace, the diagnosis was more precise: the provider was
available; the task contract was still too roomy for this model.

## The setup

The live profile was intentionally local to the dogfood worktree:

```yaml
default_profile: synthetic-claude

harness_profiles:
  synthetic-claude:
    backend: claude
    model: hf:zai-org/GLM-5.2
    models: [hf:zai-org/GLM-5.2]
    models_endpoint: https://api.synthetic.new/openai/v1/models
    quota:
      window: 1m
      tokens_per_window: 120000
      max_concurrent: 1
      reserve_tokens: 30000
      state_path: .artifacts/quota/provider-state.json
      lease_timeout: 45m
    env:
      ANTHROPIC_BASE_URL: https://api.synthetic.new/anthropic
      ANTHROPIC_AUTH_TOKEN: "${SYNTHETIC_API_KEY}"
```

The important part is not the exact limit. It is that the limit is a local
coordination contract:

- `max_concurrent: 1` meant one live GLM task could run at a time for this
  profile key.
- `reserve_tokens: 30000` gave the call a conservative starting reservation.
- `state_path` put the learned usage and in-flight lease in `.artifacts/`,
  where multiple local Kitsoki processes can coordinate without committing
  operational state.
- `lease_timeout` meant a crashed agent process would not block the profile
  forever.

The bench case was the deliver story's GLM decomposer harness:

```sh
GOCACHE=/private/tmp/kitsoki-gocache go run ./cmd/kitsoki agent-bench run \
  stories/deliver/agent-bench/decompose_glm.yaml \
  --case deliver-decompose-glm52 \
  --live \
  --json-out .artifacts/glm52-four-proposals/live-agent-bench-report.json \
  --markdown-out .artifacts/glm52-four-proposals/live-agent-bench-report.md \
  --slidey-out .artifacts/glm52-four-proposals/live-agent-bench-deck.slidey.json
```

The run drove the `stories/deliver/` decomposer against
`docs/proposals/post-host-bind-hook.md`, a real unfinished proposal. The
generated trace and reports stayed under `.artifacts/`; automated tests stayed
offline and used cassettes/flows.

## What happened

The trace confirmed the requested provider route:

- `profile=synthetic-claude`
- `model=hf:zai-org/GLM-5.2`
- one `agent.call.start`
- one `agent.call.complete`
- no `agent.call.error`
- `submit=true`

The quota state started with one 30k-token reservation:

```json
{
  "window_tokens": 30000,
  "observed_calls": 0,
  "observed_tokens": 0,
  "reservations": {
    "...": {
      "tokens": 30000,
      "expires_at": "2026-06-26T16:58:17.618465+07:00"
    }
  }
}
```

When the call finished, the reservation was gone and the state had learned from
the real usage:

```json
{
  "observed_calls": 1,
  "observed_tokens": 135991,
  "last_observed_tokens": 135991,
  "last_estimated_tokens": 30000,
  "last_rate_limited_at": "0001-01-01T00:00:00Z",
  "backoff_until": "0001-01-01T00:00:00Z"
}
```

No 429, `rate limit`, `quota`, or `too many requests` marker appeared in the
trace. No backoff was recorded. That is the graceful path: local coordination
kept the run to one pipeline, and the usage model updated after the provider
returned.

The bench score still failed:

| Metric | Result | Budget |
|---|---:|---:|
| Wall time | 707.817s | 600s |
| Input tokens | 113,008 | 150,000 |
| Output tokens | 22,983 | 16,000 |
| Total tokens | 135,991 | 170,000 |
| Cost | $1.144670 | $1.00 |
| Tool calls | 5 | 20 |
| File reads | 3 | 12 |

This is a better failure than a 429. The provider was not the blocker. The task
environment was.

## What changed because of the dogfood

The session finished four proposal slices and one dogfood bug fix:

- `/reload --force` shipped so authors can intentionally bypass `once:` during
  prompt and `on_enter` iteration while plain `/reload` remains safe.
- `demo-video-loop` adopted the `reference` prompt filter for proposal/change
  summaries, making embedded context citable in recorded prompts.
- The already-shipped observation-kind taxonomy was removed from the proposal
  queue and marked shipped in the trace-introspection epic.
- The completed GitHub Issues tracker parent epic was deleted; the only
  remaining operational bulk-migration step stayed as a standalone proposal.
- `agent-bench` now creates parent directories for `--json-out`,
  `--markdown-out`, and `--slidey-out`. The live trace was valid, but the first
  report-write attempt failed because the output directory did not exist; the
  dogfood converted that friction into a tested product fix.

The source commit for the implementation was:

```text
a7b15458 Complete four proposal dogfood slices
```

## Why this matters

Provider quota failures are usually operationally ambiguous. A user sees a
stalled pipeline, a slow model, or a 429, and the system cannot tell whether to
retry, wait, downgrade, split the job, or stop launching competing work.

The quota controller gives Kitsoki a local, provider-neutral answer:

1. Reserve before launching a CLI-backed agent.
2. Persist the reservation so sibling Kitsoki processes see the in-flight work.
3. Release and update learned token assumptions from the provider's usage.
4. Record rate-limit errors as backoff instead of blindly retrying.
5. Leave API-key profiles free to opt out or configure high limits.

That turns "synthetic.new is flaky" into one of three traceable outcomes:

- quota admitted the call and the model succeeded;
- quota held the call locally because tokens/concurrency/backoff said wait;
- quota admitted the call, but the task exceeded model-performance budgets.

This run hit the third outcome. That is actionable. The next improvement is not
"retry GLM harder"; it is to tighten the decomposer task:

- make the accepted output smaller;
- split oversized proposals before asking GLM to decompose them;
- reduce reflective narration in the decomposer prompt;
- lower the output budget only after the task contract is narrow enough;
- keep scoring offline from the recorded trace before spending again.

## Reproduce without live spend

The automated verification path remains no-LLM:

```sh
GOCACHE=/private/tmp/kitsoki-gocache go test ./internal/host -run TestProviderQuota
GOCACHE=/private/tmp/kitsoki-gocache go test ./cmd/kitsoki -run 'TestAgentBench|TestCLI_TopLevelHelp'
GOCACHE=/private/tmp/kitsoki-gocache go run ./cmd/kitsoki test flows stories/demo-video-loop/app.yaml
GOCACHE=/private/tmp/kitsoki-gocache go run ./cmd/kitsoki test flows stories/deliver/app.yaml
```

The live command should remain explicit and gated. It spends real provider
quota and exists to refresh evidence, not to make CI pass.
