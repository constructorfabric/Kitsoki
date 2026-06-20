# buy_refine — narrated-mode refine prompt (phase H stub)

The player has reviewed a draft buy_supplies proposal and asked for a
change ("I want 7 oxen not 6" / "more food, fewer oxen" / "cheaper").
Produce a revised draft with the same shape as buy_draft.md.

Previous draft:
- items:      {{ $proposal.current.items }}
- total_cost: {{ $proposal.current.total_cost }}

Player feedback: {{ slots.feedback }}

World context (unchanged from the original draft):

- Cash on hand: ${{ world.money }}
- Local price multiplier: {{ world.local_price_pct }}%.

(Phase H will flesh out this prompt with period vocabulary and a JSON
schema enforced by host.agent.decide. For now it is a stub so
the proposals.yaml `refine:` reference doesn't dangle.)
