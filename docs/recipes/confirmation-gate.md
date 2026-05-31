# Recipe: a confirmation gate before a destructive effect

**Goal:** make the user explicitly confirm before an irreversible
effect runs.

Present a two-option choice and branch: one intent applies the
destructive effect, the other cancels. Keeping the gate as its own
state makes the decision a recorded datapoint in the trace.

```yaml
intents:
  confirm: { title: "Proceed",  examples: ["yes", "proceed", "ok"] }
  cancel:  { title: "Cancel",   examples: ["no", "cancel"] }

states:
  review_delete:
    view:
      - prose: "This will delete all saved data. Are you sure?"
      - choice:
          items:
            - { label: "confirm", intent: confirm, hint: "delete everything" }
            - { label: "cancel",  intent: cancel,  hint: "keep the data" }
    on:
      confirm:
        - target: deleting
          effects:
            - invoke: host.run
              with: { cmd: "rm -rf {{ world.data_dir }}" }
              on_error: delete_failed
      cancel:
        - target: main
          effects:
            - say: "Cancelled — nothing was changed."
```

The destructive `invoke:` carries an `on_error:` arc so a failed
deletion lands in a visible `delete_failed` state rather than silently
bouncing. See the [host call & branch](host-call-and-branch.md) recipe
for that pattern.

**Reference**
- [`../stories/choice-widget.md`](../stories/choice-widget.md) — the choice widget (confirm/cancel split)
- [`../stories/state-machine.md`](../stories/state-machine.md) — states, transitions, effects
