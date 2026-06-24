# Runtime: contextual room routing and persistent room chats

**Status:** Slices 1–4 shipped (20bb2911, 9311197a, 1e062a54, 615e8c10). Remaining:
task 4.3 TUI/web UI controls (route receipt display, switch-route, rewind action);
optionally 5.2 additional flow fixtures for help/meta_edit verdict classes.
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
## 4.3 TUI/web controls (UI — not yet implemented)
- [ ] Route receipt display in TUI (class badge, reason, alternatives)
- [ ] Switch-route action (immediate re-dispatch without full rewind when state/world unchanged)
- [ ] Start new / resume lane chat controls
- [ ] Rewind action surfaced in TUI/web (calls Orchestrator.RewindRoute)
- [ ] Web UI equivalents of all of the above

## 5.2 Additional flow fixtures (optional)
- [ ] Dedicated no-LLM flow fixtures for help and meta_edit verdict classes
  (intent and room_request with plan-continuation are already covered by
  context_route_lane_test.go and context_route_plan_test.go)
```
