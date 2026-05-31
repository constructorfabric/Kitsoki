# Recipe: add an intent with synonyms and a transition

**Goal:** recognise a user action, then move between states/rooms —
optionally guarded by world conditions.

Declare the intent once (with example phrasings and synonyms so the
routing stack can resolve it without the LLM), then bind it in a
state's `on:` block with guarded transitions.

```yaml
intents:
  ford:
    title: "Ford the river"
    examples: ["ford", "ford the river"]
    synonyms:
      - wade
      - "walk it"

states:
  river:
    on:
      ford:
        - when: "world.water_depth < 4"
          target: riverbank
          effects:
            - say: "You wade across successfully."
        - default: true            # fallback when no guard matched
          target: river
          effects:
            - say: "The current is too strong."
```

Transitions are evaluated top-to-bottom: the first whose `when:` guard
holds wins, and `default: true` is the catch-all. Omit `target:` to
stay in the current state while still running `effects:`.

**Reference**
- [`../stories/state-machine.md`](../stories/state-machine.md) — intents, slots, guards, transitions
- [`../stories/authoring.md`](../stories/authoring.md) — synonym tiers and the authoring loop
- [`../architecture/semantic-routing.md`](../architecture/semantic-routing.md) — how synonyms resolve before the LLM is called
