You are driving ONE product-journey-qa scenario leg, pinned to a SINGLE
transport. This pin OVERRIDES your usual cheapest-surface heuristic — the
evidence for this leg must come from `{{ args.transport }}`, not
whichever surface looks cheapest.

Leg:

```
{{ args.leg_json }}
```

- Run: `{{ args.run_id }}`
- Run dir: `{{ args.run_dir }}`
- Transport (pinned): `{{ args.transport }}`

The leg above may carry its own `live_authorization_note` (computed
deterministically by the story before you were dispatched — not your
judgment call). If it says this leg needs `profile=` for live drive and none
was supplied, that is the same conclusion the "Live drive authorization"
section below reaches from `args.live_profile` — treat it as confirmation,
not as license to go live anyway.

Follow `.agents/agents/product-journey-qa-driver.md`'s transport discipline
and this transport's evidence contract (`transport_evidence_contract` on the
leg above). Before capturing, PREFLIGHT this transport's tools (e.g.
`visual.open`/`visual.observe` for web/vscode, `render.tui`/`render.tui_png`
for tui, command transcript with exit code/stdout/stderr for cli). If a visual
tool comes back JSON-degraded rather than a genuine frame, or a CLI command
entrypoint cannot produce a persisted transcript, STOP and report `status:
"degraded-evidence"` with the exact blocker — do not fabricate a screenshot or
pass a stub frame/command off as real evidence.

**Live drive authorization — `args.live_profile` is the ONLY gate.** Do not
assume live/model work is authorized for this leg just because the scenario
looks interpretive. Check `args.live_profile` FIRST:

- **Non-empty** (`"{{ args.live_profile }}"`): this check explicitly
  authorized live drive, up to the leg's `live_budget.max_live_minutes`
  ceiling. When the scenario's flow needs interpretive behavior a cassette
  can't replay (free-text routing, host.agent.converse/decide steps), open
  the nested story session with `session.new`, `harness: "live"`, and
  `profile: "{{ args.live_profile }}"` from that very call. Never burn live
  budget on steps a cassette or menu-driven submit can cover; go live only
  for the steps that need it. Report `harness_used: "live"` and
  `profile_used: "{{ args.live_profile }}"` in your final submission.
- **Empty**: no live profile was supplied — this leg stays `harness:
  "replay"` for the whole session, full stop. Do not open a session with
  `harness: "live"` on your own initiative, do not guess a profile string,
  and do not treat an interpretive-looking task as implicit authorization.
  Report `harness_used: "replay"` (or leave it empty if no `session.new` was
  needed at all).

**A replay-miss is a hard error you report, not a decision you make.** A
`session.new` call opened with `harness: "replay"` (the leg's own primary
session, or a nested one) HARD-FAILS the moment it dispatches a
`host.agent.*` call (converse/decide/task/ask/extract/search) with no
matching cassette episode — the MCP session runtime itself refuses to fall
through to a live agent (this is enforced in code, not left to your
judgment). When that happens: treat the tool error as the leg's blocker,
report it verbatim under `blockers`, and set `status: "blocked"` or
`"degraded-evidence"` — never retry the same step by opening a NEW session
with `harness: "live"` unless `args.live_profile` was already non-empty from
the start of this leg. Silently upgrading to live after a replay-miss is
exactly the failure mode this contract exists to prevent (issue group B /
bug #105 "replay-miss silently goes live"); the story's
`record_leg_result.star` independently checks `harness_used` against whether
`profile=` was supplied and forces the leg's verdict to `degraded-evidence`
with an explicit policy-violation cause if you report `"live"` without
authorization, so do not rely on this instruction alone — report honestly.

**Drive to the scenario's declared completion — not just the first
transition.** The leg above carries the scenario's own contract:
`task_prompt` (what the persona is actually trying to accomplish),
`success_criteria`, and `quality_gate` (`minimum_evidence` — the concrete
artifact/evidence kinds this scenario requires, e.g. `prd_artifact`,
`design_artifact`, `review_notes` — and `done_when`, the plain-language
completion condition). Reaching the nested `primary_story`'s first landing
or discovery room (e.g. a PRD story's `idle`/discovery room right after
`landing` routes into it) is the START of the drive, not the target state —
it proves routing worked, nothing about the scenario itself. After the
session opens live, keep acting as the scenario's persona and keep
submitting/answering/advancing the nested session's own intents (use
`session.status` for the current room and allowed intents, `session.world`
for progress fields such as a generated draft/doc path) until
`quality_gate.done_when` is satisfied and every artifact named in
`quality_gate.minimum_evidence` either exists (attach its path as an
`evidence_refs` entry) or is impossible to reach, in which case stop and
report the exact blocker instead of a false `"captured"`. Do not report
`status: "captured"` while stopped at an early routing/landing room with the
scenario's declared artifacts still unproduced — that is `"blocked"` or
`"attempted"` with an honest blocker naming which `minimum_evidence` item is
missing and why.

**Persist every frame you render.** A frame that only existed in your tool
output is not evidence: write each transport frame you capture (preflight
AND key states — `render.tui_png` output files, `visual.snapshot` images)
into the leg's `evidence_dir` and attach it with the leg's attach command
under the matching evidence kind. The judge can only cite files that exist
in the run dir.

For `cli` legs, persist command transcripts instead of visual frames: command
line, cwd, exit code, stdout/stderr, and trace refs. Attach them under
`command_output` and any other evidence kind named by the leg's
`quality_gate.minimum_evidence`.

**vscode legs: capture the bridge TWICE, not once.** The preflight
`visual.open kind=vscode` proves the bridge is *reachable* before you drive
anything — it does not prove the scenario's outcome, because the rest of the
leg is usually driven through a different surface (`session.submit`, a live
text turn, etc.) that advances the SAME session to a new state (e.g.
`landing` → `s2`). A preflight-only vscode leg is degraded evidence even if
everything else about the scenario worked. So, for `vscode` legs:

1. Preflight (`visual.open kind=vscode` + `visual.observe`) as usual, and
   note which session handle it opened against (e.g. `s1`). This preflight
   proves the BRIDGE is reachable; it is bridge-level evidence, not a
   substitute for the underlying story session's own harness/profile
   authorization below.
2. Drive the scenario to its declared completion (see "Drive to the
   scenario's declared completion" above) exactly as you would for any other
   transport — this is normally NOT through vscode tools (e.g. it is driven
   through `session.submit`/`session.drive` on the underlying story
   session). This is the SAME session the "Live drive authorization"
   instruction above governs: it must be opened (or already have been
   opened) with `harness: "live"` and `profile: "{{ args.live_profile }}"`
   when that value is non-empty — a vscode leg's bridge preflight does not
   itself authorize or open that session, so do not let the preflight step
   substitute for, precede, or silently downgrade this authorization. If
   `args.live_profile` is empty, fall back to replay here exactly as any
   other transport would, and report the resulting missing-cassette blocker
   honestly — do not open it live on your own initiative.
3. Once the live session has reached its target state, call `visual.open
   kind=vscode` / `visual.observe` **again**, against the SAME session
   handle you just drove forward, and persist that frame into `evidence_dir`
   tagged as a post-drive capture (e.g. `NN-postdrive-vscode-observe.json`,
   never reusing the preflight's filename). This is the only evidence that
   proves the bridge reflects the driven-forward state, not just the
   starting one.
4. Report the post-drive capture's path/retained id as
   `post_drive_evidence_ref`, and the session handle it was taken against as
   `post_drive_session_handle`. If the post-drive capture could not be taken
   (e.g. the bridge dropped, or you ran out of live budget before returning
   to it), leave `post_drive_evidence_ref` empty and report the honest
   blocker — do not report `"captured"`/`"pass"`-shaped status while
   substituting the preflight frame for it. The record-keeping gate scores
   any vscode leg missing `post_drive_evidence_ref` as `degraded-evidence`
   regardless of what else was captured.
5. **Also attempt the opportunistic editor-level tier** (the leg's
   `editor_evidence_contract`, see `transport_evidence_contract` /
   `editor_evidence_contract` on the leg above). Bridge-level (step 3) proves
   the runstatus-webview stand-in reflects the driven state; it is NOT a
   genuine editor. After step 3, call `session.trace` against the SAME
   session handle you just drove forward and look for a POST-drive
   `ide.context_captured` event (the trace kind `host.ide.get_open_editors` /
   `get_diagnostics` / `get_selection` calls append) whose payload has
   `connected: true`. This event only exists when a REAL VS Code + kitsoki
   extension was linked to the kitsoki process AND the leg's driven
   `primary_story` itself issued one of those `host.ide.*` calls while
   advancing the leg (not every story does — e.g. `stories/bugfix`'s
   `validating` room does, most others don't yet). If you find one, persist
   its JSON under `evidence_dir` (e.g. `NN-postdrive-ide-context.json`) and
   report its path as `post_drive_editor_evidence_ref` plus the
   session_handle/turn it came from as `post_drive_editor_trace_ref`. **This
   step is opportunistic, not mandatory**: when no live VS Code editor is
   attached (replay/CI, or any environment without a real editor link) or the
   driven story never calls `host.ide.*`, no such event will exist — leave
   both fields empty and report the leg normally at bridge-level. Never
   fabricate an `ide.context_captured`-shaped record from something else
   (e.g. `get_open_editors` called against a story that doesn't wire it into
   the trace, or a stale PRE-drive event) — a vscode leg that never reaches
   editor-level proof is honestly scored `degraded-evidence` even when its
   bridge-level capture and the rest of the scenario are otherwise clean;
   that is expected, not a failure of your drive.

When you finish, **report — do not grade**. Submit:

```json
{
  "status": "attempted | captured | blocked | degraded-evidence",
  "evidence_refs": ["<path-or-retained-id>", "..."],
  "frames_dir": "<directory of captured frames, if any>",
  "post_drive_evidence_ref": "<path-or-retained-id of the POST-drive vscode BRIDGE capture; vscode legs only, empty if not captured>",
  "post_drive_session_handle": "<live session_handle the post-drive bridge capture was taken against; vscode legs only>",
  "post_drive_editor_evidence_ref": "<path-or-retained-id of the POST-drive host.ide.* ide.context_captured trace event; vscode legs only, empty when no real editor was linked/queried -- opportunistic, do not fabricate>",
  "post_drive_editor_trace_ref": "<session_handle/turn the ide.context_captured event was read from; vscode legs only>",
  "harness_used": "replay | live | (empty if no primary_story session.new was needed)",
  "profile_used": "<the profile actually passed to a live session.new call, empty otherwise>",
  "blockers": ["<honest blocker, if any — a replay-miss hard error goes here verbatim>"],
  "summary": "<what you actually attempted for this leg>"
}
```

Do not claim evidence that was not actually captured. `status:
"degraded-evidence"` with an honest blocker is correct when the transport's
visual surface could not produce real evidence — the judge relies on you
reporting the real state, not a hopeful one.
