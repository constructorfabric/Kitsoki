# Recipe: collect structured input with a choice form

**Goal:** gather several typed fields in a single submission instead of
asking one question per turn.

Use the choice widget in `form` mode. The widget validates each field
(type, `min:`/`max:`, `required:`) and submits **one** intent carrying
all fields as slots, so your transition sees them together.

```yaml
intents:
  propose_purchase:
    title: "Purchase supplies"
    slots:
      items:      { type: string, required: true }
      total_cost: { type: int,    required: true }

states:
  general_store:
    view:
      - choice:
          mode:     form
          prompt:   "Compose your purchase"
          intent:   propose_purchase
          template: "Buy {items} for ${total_cost}."   # static preview
          fields:
            items:
              type: string
              placeholder: "oxen, food, wheels"
              required: true
            total_cost:
              type: int
              min: 1
              max: "{{ world.money }}"                  # bounds can be templated
    on:
      propose_purchase:
        - when: "slots.total_cost <= world.money"
          target: purchase_done
          effects:
            - set: { money: "{{ world.money - slots.total_cost }}" }
        - default: true
          effects:
            - say: "Not enough money."
```

`min:`/`max:` are enforced by the widget before submit, so an
out-of-bounds value never reaches your transition. Keep `template:`
short — it is also what static transports (Bitbucket, plain log) render.

**Reference**
- [`../stories/choice-widget.md`](../stories/choice-widget.md) — single, multi, and form modes; field types and bounds
- [`../stories/state-machine.md`](../stories/state-machine.md) — intent slots and reading `slots.*` in guards/effects
