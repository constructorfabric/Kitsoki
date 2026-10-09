# Concept

Kitsoki is a conversational workflow engine built on one commitment: **make
workflows as deterministic as possible, and confine the LLM to narrow,
identified, traceable decision points or tasks.**

This document is the thesis. It explains *why* kitsoki is shaped the
way it is. For the runtime mechanics — rooms, intents, guards, the
turn loop — see [`architecture.md`](overview.md) and
[`state-machine.md`](../stories/state-machine.md).

---

## 1. The problem

Two extremes dominate today's interactive software.

**Traditional CLIs** are fully deterministic and unforgiving. Every
decision is encoded as flags, paths, JSON shapes. The user has to
match the encoding exactly; one missing flag and nothing happens.
The compensating virtue is total predictability: once the syntax is
right, the program does exactly what it said it would, and you can
read its source to know what that is.

**Chat agents** are the opposite. The user can say anything; the agent
figures it out. The compensating cost is that *every decision becomes
a moment of LLM judgment*. Which tool? Which arguments? Which order?
Whether to ask first? The LLM holds the reasoning state, the tool-use
plan, the conversational frame — all of it, on every turn. When
something goes wrong it's usually unclear *where* it went wrong: was
the prompt under-specified, did the model mis-rank the tools, did the
user's phrasing edge into adjacency?

Kitsoki takes the forgiving entrance of a chat agent and the
predictability of a CLI, and gets there by **inverting control**
between the LLM and the runtime.

The competitive claim is deliberately narrow: Kitsoki is for workflows where
"the model probably does the right thing" is not enough. If a run needs operator
handoff, policy gates, durable state, replay, cost control, or a defensible audit
record, the control boundary has to live outside the model.

---

## 2. Control inversion

In a typical LLM-driven system, the **LLM is in charge** and the
runtime is its tool belt. The LLM holds the plan; the runtime exposes
capabilities; the LLM picks which capability to invoke and when. The
runtime executes.

```mermaid
flowchart LR
    llm["LLM<br/>plan, reason, decide"]
    runtime["Runtime<br/>execute tools"]
    llm -->|"calls"| runtime
```

Kitsoki flips this. The **runtime is in charge** — a YAML state
machine, written by the application author, that knows every room
the conversation can be in, every intent valid in each room, every
transition out, every effect that fires on each transition. When the
runtime needs help with something it cannot resolve deterministically
it *calls the LLM* for that narrow sub-task, with maximum context and
tightly-scoped tooling, then takes the result and resumes
deterministic execution.

```mermaid
flowchart LR
    runtime["Runtime<br/>state machine, transitions, effects"]
    llm["LLM<br/>narrow domain, scoped tools"]
    runtime -->|"resolve this sub-task"| llm
    llm -->|"named intent / typed payload / artifact"| runtime
```

The arrow that matters is the LLM's *return* arrow. It produces a
named intent (or a typed payload, or a finished artifact). It does
not write the world, it does not pick the next room, it does not fire
effects, it does not call hosts. Those are the runtime's job, and
they happen only along edges the author declared.

That is the point a structured-output wrapper cannot reach on its own. Schema
validation can prove that one response has the right shape. It cannot prove that
the model was allowed to make only this decision, that the next transition was
declared before the call, that a rejected submission was handled inside a
bounded retry loop, or that the same conversation can replay later without a
live model.

This is "control inversion" in the same sense as dependency injection
turns over object construction: the thing that used to be on top (the
LLM) becomes a callee, and the thing that used to be the tool belt
(the runtime) becomes the caller.

---

## 3. Narrow LLM domains

Once the runtime is in charge, the question becomes: **where exactly
do we still need the LLM?** Every place we need it is a decision point
— a moment where the deterministic graph can't continue without
interpretation.

Kitsoki's commitment is that those points are **identified, named,
and traceable**, not scattered across an opaque reasoning loop. There
are three shapes:

| Shape | What the LLM does | Example |
|---|---|---|
| **Routing** | Map free conversation onto one of a finite, author-declared set of intents valid in this room. | "scale the frontend to three" → `scale{service:frontend, replicas:3}`. |
| **Interpretation** | Extract structured form-data from free text, or summarize a structured artifact into prose. | A bug-repro paragraph → typed fields (`steps`, `expected`, `actual`). A diff → a one-line PR title. |
| **Tasks** | Execute focused work in a sandboxed domain with maximum context and a small toolbox. | "draft the fix for this bug." An agent runs Claude Code in a worktree, with the bug file, the repo, and a fixed toolset; it returns when its phase is done. |

In every case the LLM is given the **smallest reasonable domain of
action**, the **maximum context relevant to that domain**, and a
**focused tool set for the exact case at hand**. It is never asked
to "figure out what to do next" at the conversation level — that's
the state machine's job.

This is also the leverage point for frontier models. Kitsoki is not a
claim that strong agents are unnecessary; it is a way to use them where their
reasoning matters most. A frontier model should spend its long context and
planning horizon on synthesis, oversight, hard judgment, and cross-step
coordination. Repeatable action should move into high-leverage tools around it:
scripts and tests for deterministic execution, typed host calls for bounded
side effects, cheaper model calls for narrow extraction or classification, and
structured decomposition for long tasks that need reviewable handoffs. The
runtime multiplies the agent by giving it better instruments than raw free-form
tool use.

The result is that every LLM call has a defined input shape, a
defined output shape, and a defined position in the trace. When
something goes wrong, an operator can point at the specific call that
produced the wrong result and look at the prompt, the world snapshot,
the tools the LLM had available, and the output it returned. There
is no "what was the agent thinking" — only "what did this call
receive and emit".

### Interpretation has two shapes — and one is better

The interpretation case is worth a closer look, because the same
output (a well-typed artifact) can arrive by two very different
paths, and they are not equally trustworthy.

**The LLM writes a script or test that does the interpretation.**
The LLM's work product is a *reproducible artifact* — a failing
test, a small extraction script, a SQL query, a parser — that, when
run, produces the structured result. The script is checked into the
repo (or the session). It can be reviewed to know exactly what it
does. It can be re-run on demand and produces the same result every
time. It can be reused for validation and regression testing long
after this turn is forgotten.

The clearest case is evidence-based bug reproduction. The LLM's job
in the `reproduce` phase is not to declare "yes, this bug is real",
and it is *also* not to fill in a `{steps, expected, actual}` JSON
form. Its job is to *write a failing test that demonstrates the bug*,
plus a short narrative explaining why. The test carries the
interpretation forward in a form anyone can re-execute — and stays
as a regression guard once the fix lands.

**The LLM directly produces a well-typed object.** The LLM emits a
structured payload that validates against a schema — a `BugReport
{steps, expected, actual}`, a `Summary {tldr, key_points}`, an
extracted invoice. Schema validation catches malformed output, but
the *interpretation itself* was a one-shot LLM act: re-asking the
same question on slightly different inputs produces a slightly
different object. There is no re-runnable test; there is only this
one verdict.

Both produce well-typed artifacts. The difference is what carries
trust forward:

| Shape | What is reviewable | What carries trust forward |
|---|---|---|
| **Script / test** | The script source. Same input, same result, forever. | Re-run the artifact. |
| **Schema-validated object** | The prompt, the world snapshot, the rationale, the output. | Audit this one instance. |

**Prefer the script-producing form, even when a schema'd object
would validate.** A failing test is more useful than a structured
bug report. A SQL query against `pg_stat_statements` is more useful
than an LLM-summarised "slow queries" object. A small script that
extracts invoice fields by regex is more useful than an LLM
extraction call, once the regex stops needing to evolve. Each
upgrade replaces a recurring interpretation cost with a one-time
authorship cost and a deterministic re-run — and the artifact stays
around as documentation of *how* the interpretation is done, not just
*what* it produced this time.

When the work genuinely does not admit a script — open-ended free
text summarisation, ranking by judgment, classification with
ambiguous boundaries — the typed-object form is the right tool. The
input and rationale are preserved in the trace so a reviewer can
audit *this instance*. But the bar for "this work doesn't admit a
script" is higher than it first appears, and a fair amount of
[progressive determinism](#4-progressive-determinism) is the slow
discovery that more cases than expected actually do.

---

## 4. Progressive determinism

The point of all this is not to write deterministic flows from
scratch. Most useful workflows don't *start* as deterministic flows —
they start as an idea, a description, a "wouldn't it be nice if".
Kitsoki is built around the lifecycle that takes an idea and grows
it into something predictable.

```mermaid
flowchart TD
    idea["Idea / description"]
    prove["Prove the workflow end-to-end<br/>LLM does most of the work<br/>trace records every decision"]
    inspect["Identify recurring decision points<br/>read the trace for repeated model judgment"]
    convert["Convert prompt to flow<br/>replace prompt prose with rooms, intents,<br/>transitions, and effects"]
    deterministic["More deterministic workflow"]

    idea --> prove --> inspect --> convert --> deterministic
    deterministic -. "loop back" .-> inspect
```

At each iteration:

- **What was a prompt instruction becomes a deterministic flow.**
  "Ask the user whether to commit to main or develop" stops being a
  paragraph in a system prompt and becomes a room with two intents
  and two outgoing transitions.
- **What was a free-form LLM tool call becomes a host invocation with
  a typed contract.** "Run the build and tell me if it passed" stops
  being a tool-use plan and becomes `invoke: host.run` with a declared
  command and a parsed exit code.
- **What was a one-off interpretation becomes a slot template.** "Buy
  3 widgets for $12" stops being a per-turn LLM round-trip and becomes
  a synonym template that captures `items` and `total_cost` without
  an LLM call.
- **What was an LLM interpretation becomes a script or test.** The
  LLM stops being asked to *judge* whether the bug is real and starts
  being asked to *write the failing test that proves it*. The test
  then runs deterministically on every iteration and stays as a
  regression guard once the fix lands. This applies even when the
  judging form would have produced a well-typed object — a re-runnable
  artifact is always a stronger gate than a one-shot verdict.

Each conversion is **local and reviewable**: a diff against the YAML
tree, a measurable change in trace shape, a measurable reduction in
LLM cost and latency. The author keeps the LLM where it's still
earning its keep and replaces it everywhere it's become predictable.

This is what we mean by **predictable continuous improvement.** Other
LLM systems improve by tweaking prompts and praying. Kitsoki improves
by turning recurring LLM decisions into deterministic edges — one
decision point at a time, each conversion auditable, each backed by
trace evidence.

The reverse direction is also explicit: when a deterministic flow
turns out to be wrong (the intent vocabulary missed a case, a slot
template doesn't parse what users are actually saying), the trace
shows it — the LLM was invoked when it shouldn't have been, or the
human-escape was hit when it shouldn't have been — and the author
widens the deterministic surface to match.

---

## 5. The spectrum of stories

Because kitsoki controls how much of a workflow is deterministic and
how much is LLM-mediated, the same engine works across a wide range
of shapes.

### Forgiving CLI wizards

At one end: a CLI that is mostly menus and explicit choices, with the
LLM acting as a friendly front door. The user types `tickets` or
`show me everything open assigned to me` — both arrive at the same
room. When the LLM misreads, the deterministic menu is still there
as a fallback, and the user picks an action by number. The LLM is
one of four routing tiers; on most turns it isn't called at all.

This is the kitsoki-dev dogfood instance, the cloak demo, the
oregon-trail story. The deterministic graph carries most of the
weight; the LLM polishes the entrance.

### Open-ended agent workflows

At the other end: a bug-fixing or PR-refinement pipeline where,
inside a `propose` or `implement` room, an agent acts like a
relatively free Claude Code session — reading the repo, drafting
changes, running tests — but inside a **room** with an explicit
checkpoint at the end. When the phase completes, control returns to
the deterministic state machine: the user (or an LLM judge with
confidence thresholds) reviews the artifact, accepts or refines it,
and the conversation moves on to the next deterministic phase.

This is the bug-fix pipeline in `.kitsoki/stories/kitsoki-dev/`: reproduce →
propose → implement → test → review → validate → done → PR refinement
→ merge. Each room can host as much LLM latitude as the author
thinks the task deserves; the *boundaries between rooms* stay
deterministic.

### Everything in between

Most useful workflows live in the middle. A proposal-drafting flow
might have deterministic routing into a room, a free-form drafting
session inside the room, and a deterministic review-and-iterate loop
around it. A data-extraction flow might be entirely typed slot
templates with an LLM fallback for the long-tail. The author picks
how much determinism each phase deserves and adjusts as the trace
teaches them.

**The dynamic nature is the point.** Authors *build the story as they
go*: start with an LLM-heavy sketch that proves the workflow, extract
the parts that have settled into deterministic rooms, leave the parts
that genuinely need interpretation as LLM calls. The story expands
with the author's understanding of the domain.

---

## 6. What this buys

A few consequences fall out of the commitment.

- **Auditability.** Every decision the system made is in the event
  log: which intent was picked, which guard matched, which host
  returned what, which LLM call produced which output. An operator
  can replay any session offline; a reviewer can diff a YAML tree.
- **Testability.** Because the LLM call is a bounded sub-task with a
  typed output, it can be replaced by a recording. Flow tests run
  with zero LLM cost and exit non-zero on regression.
- **Cost and latency control.** The deterministic surface — synonym
  matches, slot templates, the turncache — handles most input without
  an LLM call. On a typical story ~78% of recorded turns route
  deterministically.
- **Improvement is incremental.** Each iteration is a diff against
  the YAML tree, not a global prompt rewrite. The trace tells the
  author which decision points are worth promoting to deterministic
  flows next.
- **The LLM contributes its strengths and is denied its weaknesses.**
  Natural-language understanding inside a bounded sub-task is what
  current LLMs are best at. Open-ended cross-turn planning is what
  they are worst at. Kitsoki uses the first and refuses to rely on
  the second.

---

## 7. What kitsoki is not

- **Not a chat agent.** The LLM has no latitude to invent actions
  outside the intent alphabet declared by the room.
- **Not a general workflow engine.** Kitsoki's graph is shaped for
  *conversation* — typed turns, surfaces, off-path escapes, mid-flight
  clarifications — not for the data-pipeline shapes Temporal /
  Airflow / Step Functions are built around.
- **Not production software.** Kitsoki is a PoC under internal
  validation. The architectural commitments above are stable; the
  surface area is still moving.

---

## 8. Where to go next

- **[`architecture.md`](overview.md)** — the runtime: layers,
  packages, the turn loop, persistence, transports.
- **[`state-machine.md`](../stories/state-machine.md)** — the vocabulary: rooms,
  intents, slots, world, guards, transitions.
- **[`authoring.md`](../stories/authoring.md)** — how to write an `app.yaml`.
- **[`semantic-routing.md`](semantic-routing.md)** — the routing
  stack between deterministic match and LLM call, and how authors
  grow the synonym library from traces.
- **[`testing.md`](../tracing/testing.md)** — the two test modes (intent
  pass-rate vs. deterministic flow) that fall out of the architecture.
