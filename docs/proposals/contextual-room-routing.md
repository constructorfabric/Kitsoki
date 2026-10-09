# Runtime: contextual room routing and persistent room chats

**Status:** Mostly shipped. Runtime slices 1–4 shipped (20bb2911, 9311197a,
1e062a54, 615e8c10), web route receipt + rewind plumbing exists, and
`Orchestrator.RewindRoute` now covers lane and intent-class redispatch paths.
Remaining: switch-route ergonomics, TUI parity for receipt/rewind controls, and
optional extra no-LLM fixtures for help/meta_edit verdict classes. The
single-decision-point direction (one shared routing stack instead of a
TUI pre-pass plus `Turn`/`OneShot`) shipped as the never-silent runtime
— see [`semantic-routing.md`](../architecture/semantic-routing.md#13-synonym-templates)
(the proposal, `never-silent-runtime.md`, was deleted per lifecycle
guidance once its only open item shipped).
**Kind:**   runtime
**Epic:**   — standalone
**Relation:** builds on [`ad-hoc-structured-plan.md`](ad-hoc-structured-plan.md)
and the shipped meta-mode / agent-off-ramp model.

The shipped design is fully documented in
[`docs/architecture/semantic-routing.md` §7](../architecture/semantic-routing.md#7-contextual-routing-tier)
(routing tier, four classes, room chat lanes, plan-continuation guard, receipt,
rewind, no-LLM replay).

## Remaining work

```
## 4.3 TUI/web controls
- [ ] Route receipt display in TUI (class badge, reason, alternatives)
- [ ] Switch-route action (immediate re-dispatch without full rewind when state/world unchanged)
- [ ] Start new / resume lane chat controls
- [ ] Partial: rewind action surfaced in web (calls Orchestrator.RewindRoute); TUI parity still open
- [ ] Partial: web UI equivalents of all of the above are partial: receipt/rewind exists, switch-route/start-resume controls still need review

## 5.2 Additional flow fixtures (optional)
- [ ] Dedicated no-LLM flow fixtures for help and meta_edit verdict classes
  (intent and room_request with plan-continuation are already covered by
  context_route_lane_test.go and context_route_plan_test.go)
```
