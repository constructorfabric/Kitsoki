---
stepsCompleted: [1, 2, 3, 4, 5, 6]
inputDocuments: [market-research.md]
workflowType: research
lastStep: 6
research_type: domain
research_topic: kitsoki — LLM-assisted, audit-grade engineering workflows
research_goals: Map the engineering-workflow domain kitsoki targets; surface domain-specific pain points and validation for the value proposition
user_name: Brad Smith
date: 2026-05-19
web_research_enabled: true
source_verification: true
---

# Domain Research: LLM-Assisted, Audit-Grade Engineering Workflows

**Date:** 2026-05-19
**Author:** Brad Smith
**Research Type:** Domain Research
**Subject:** kitsoki

---

## Research Overview

Market research (see [`market-research.md`](market-research.md)) located kitsoki on the 2026 conversational-AI landscape. This document drops one level deeper: into the actual *engineering domain* kitsoki was designed against — LLM-assisted software engineering workflows that have to be auditable, reproducible, and runnable across multiple surfaces (local TUI, Jira ticket comments, Bitbucket PR comments).

The domain is currently being reshaped by two converging forces:

1. **AI coding assistants reached scale.** At Anthropic, ~90% of Claude Code's own source is now written by Claude Code ([Addy Osmani, 2026][addy-osmani-2026]). Across the broader industry, AI-assisted engineers ship 21% more tasks and 98% more pull requests per person ([Cortex, 2026][cortex-2026]). The base rate of LLM-driven engineering output is no longer marginal.
2. **Governance has caught up and is enforcing.** The EU AI Act, NIST AI Risk Management Framework, and ISO 42001 are no longer guidance — they carry enforcement weight, and regulated industries add 20–35% to base build cost specifically to satisfy these requirements ([Glean, 2026][glean-compliance-2026]).

The collision of those two forces — high AI-leverage *and* high audit pressure — is the domain kitsoki lives in. This research characterises that domain's practitioners, workflow shape, pain points, and trends, and validates that kitsoki's architectural choices map cleanly onto domain expert mental models.

---

## 1. Domain Definition and Scope

### 1.1 Domain boundary

The domain kitsoki addresses is *not* general "conversational AI." It is specifically:

> Software engineering workflows in which (a) a human engineer or LLM operator drives a multi-step process — bug fix, feature implementation, refactor, release — through natural language, (b) the process must be reproducible step-for-step under audit, and (c) the process spans multiple surfaces (local terminal, ticket-tracker comments, pull-request comments) but shares one logical conversation per session.

The boundary excludes:

- Customer-service chatbots (different buyer, different audit posture).
- Pure code-generation assistants (Copilot, Cursor, Aider) — they edit code, but they don't *drive a workflow* or share state across surfaces.
- Generic agent frameworks (LangGraph, CrewAI) — they execute LLM plans but do not impose author-declared state graphs.

The boundary includes:

- Internal dev-platform tools at regulated enterprises that have to defend their LLM workflows in audit.
- Ticket-driven bug-fix pipelines where the ticket itself is the audit record.
- Multi-environment release/promotion workflows where a single conversation should survive surface switches.
- "Human-guided coding agent" patterns — the consensus 2026 design where the LLM operates *within* a deterministic flow that always reasserts control ([Sobonix, 2026][sobonix-2026]; [Akka, 2026][akka-frameworks]).

### 1.2 Why the domain is distinctive

Three properties distinguish this domain from adjacent ones:

- **The artifact is the conversation.** In a bug-fix pipeline, the bug file (or Jira ticket) accumulates "Comment <iso> by <author>" blocks that *are simultaneously* the ticket, the audit log, and the chat history. There is no separate transcript store. This is unusual — most chat systems treat the transcript as ephemeral or as a separate database.
- **The engineer is also the LLM operator.** Unlike customer-service AI (where the bot serves an external customer), here the same person who wrote the YAML app is the person driving it. Tooling ergonomics matter more than turn-taking psychology.
- **Reproducibility is the unit of trust, not response quality.** A wrong answer is recoverable. A non-replayable answer is not.

---

## 2. The Domain Practitioner

### 2.1 Practitioner profile

The domain expert kitsoki is built for is the **regulated-industry software engineer or staff engineer who has been made responsible for the AI-assisted workflow on their team.** Composite profile (synthesised from [Cortex 2026][cortex-2026], [Glean 2026][glean-compliance-2026], [Datawiza 2026][datawiza-2026]):

- 8–15 years engineering experience.
- Sits in a regulated vertical: financial services, healthcare, public sector, telecom, infrastructure, or — most relevantly to Acronis — cybersecurity / data protection.
- Has personally written or owned a system that had to pass an SOC 2 / ISO 27001 / FedRAMP / HIPAA audit.
- Reports to engineering leadership that has been *told* by legal or compliance that "the AI usage must be auditable" but has not been told *how*.
- Is fluent enough in Python/Go/TypeScript that an OSS framework is preferable to a SaaS contract.

### 2.2 What the practitioner already does

Composite workflow pattern, drawn from [Addy Osmani's 2026 workflow][addy-osmani-2026], [Sobonix's human-guided coding-agent thesis][sobonix-2026], and [Cortex's AI-tools survey][cortex-2026]:

1. **Specify before coding.** Detailed written spec — increasingly a prompt-shaped doc — before any model is invoked.
2. **Break work into small iterative chunks.** Few-line diffs, frequent commits.
3. **Provide extensive context.** Whole-file reads, related-file context, prior-conversation context.
4. **Pick the right model.** Different models for cheap vs. expensive turns (Sonnet for routine, Opus for hard, Haiku for cheap).
5. **Maintain oversight via tests and code review.** Tests are the LLM's tightest leash.
6. **Commit frequently.** Version control as the rollback surface.
7. **Customise AI behaviour with rules and examples.** `CLAUDE.md`, `.cursorrules`, project-level prompts.
8. **Automate quality gates.** CI runs lint, tests, security scans on every change.

Three of these eight (specify-first, oversight-via-tests, customise-with-rules) are direct architectural cousins of kitsoki's `app.yaml`, deterministic flow tests, and intent-alphabet declaration.

### 2.3 What the practitioner cannot currently do well

From the same sources, surfaced as recurring complaints:

| Domain pain point | Source | Kitsoki posture |
|---|---|---|
| "AI coding agents don't fully comprehend context, governance, and accountability." | [Sobonix 2026][sobonix-2026] | Kitsoki forces the LLM into a declared intent alphabet — context boundaries are encoded as state graph |
| "Without audit trails, AI adoption becomes difficult to govern at scale." | [Custodia / DEV 2026][custodia-audit-trail-2026] | Kitsoki's event log + on-disk artifact-as-conversation is the audit trail |
| "If every request appears under the same shared credential, it is difficult to prove who accessed which model, when, and for what app or agent." | [Datawiza 2026][datawiza-2026] | Kitsoki's session model + per-transition recording solves the same problem at a different granularity |
| "AI-assisted programming decreases the productivity of experienced developers by increasing the technical debt and maintenance burden." | [arXiv 2510.10165][ai-productivity-paradox] | Kitsoki's "no out-of-state actions" constraint pushes LLM output into a typed, validated channel that resists debt accumulation |
| "Compliance frameworks (EU AI Act, NIST AI RMF, ISO 42001) carry real enforcement weight in 2026." | [Glean 2026][glean-compliance-2026] | Kitsoki's deterministic replay is direct evidence for the "explainability" and "reproducibility" pillars these frameworks demand |

---

## 3. Workflow Anatomy in the Target Domain

### 3.1 The canonical bug-fix workflow kitsoki encodes

Kitsoki's `.kitsoki/stories/kitsoki-dev/` dogfood instance encodes this exact workflow as an 8-room pipeline: **reproduce → propose → implement → test → review → validate → done → PR refinement → merge** ([`README.md`][readme]). Each checkpoint appends a `## Comment <iso> by <author>` block to the bug file. The bug file is the conversation log is the audit trail.

This is not idiosyncratic — it mirrors the published 2026 consensus for AI-assisted engineering workflows. [Sobonix's 2026 "rise of human-guided coding agents" thesis][sobonix-2026] describes essentially the same pipeline, and [Addy Osmani's workflow post][addy-osmani-2026] reports its production use at scale. The novelty in kitsoki is not the pipeline shape; it is that the pipeline is *declared in YAML, replayable in CI, and runs across three surfaces without code duplication*.

### 3.2 Multi-surface conversation as a domain requirement

A domain-specific constraint that adjacent markets (customer service, chat) do not impose is **surface-switching mid-conversation**. An engineer might:

1. Start a bug-fix conversation in the local TUI while debugging on their laptop.
2. Hand the next checkpoint to a teammate via Jira ticket comment.
3. Continue the review through Bitbucket PR comments.
4. Return to local TUI to validate.

All four surfaces should share *one logical state machine instance* keyed by external thread ID. The domain expert mental model is "one conversation, four surfaces" — not "four parallel conversations to reconcile later."

Kitsoki implements this directly via its transport abstraction ([`docs/architecture/transports.md`][transports]). Sessions are keyed by external thread, so a Jira comment posted in response to a TUI checkpoint resumes the same state machine. None of the major dialogue-manager competitors (Rasa, Dialogflow CX, LangGraph) offer this — they assume a single front-end channel per conversation.

### 3.3 The author–operator–auditor triad

A characteristic of the domain that's absent from adjacent markets is the **three-role mental model**:

- The **author** writes the YAML app — declares intents, slots, states, transitions, guards, world.
- The **operator** (often the same engineer, sometimes an LLM driving via MCP) supplies free text into the running app.
- The **auditor** (compliance, security, customer, regulator) replays the session from the event log months later.

A domain expert designs for all three. Most LLM frameworks design for only the operator. Most workflow engines design for only the author. Kitsoki's audit-trail-as-bug-file design forces the auditor role into the architecture from day one — and the dogfood instance proves this is liveable, not theoretical.

---

## 4. Domain Trends and Pressures (2026)

### 4.1 Trend 1: "Human-guided" overtakes "autonomous" as the dominant architecture

The 2026 consensus is that fully-autonomous coding agents have plateaued and the practical winner is *human-guided AI coding* — agents that operate within explicit task boundaries set by a person ([Sobonix 2026][sobonix-2026]). This is structurally identical to kitsoki's "free-text in, declared intent alphabet" — the LLM is given autonomy within a boundary, not outside it.

### 4.2 Trend 2: Audit-trail as table-stakes

LLM audit trails are now defined as "durable, tamper-evident, context-rich ledgers spanning the entire lifecycle... allowing forensic reconstruction of what happened, when, and who authorized it" ([Custodia / DEV 2026][custodia-audit-trail-2026]). Enterprise tools (JetBrains AI Assistant, Claude for Teams, Harness) all ship with audit logs, RBAC, SSO ([JetBrains 2026][jetbrains-2026]). The audit-trail-as-bug-file pattern kitsoki already implements is the domain-expert mental model in this trend made concrete.

### 4.3 Trend 3: Per-developer LLM API usage controls

Engineering teams are deploying per-developer LLM cost controls and access scoping ([Datawiza 2026][datawiza-2026]). The four-tier semantic-routing stack kitsoki already ships (synonyms → templates → turncache → LLM, with 78% of turns avoiding the LLM) is direct support for this pattern: it reduces LLM cost not by quota but by architecturally avoiding the call.

### 4.4 Trend 4: "Specifications-as-prompts" emerging as a discipline

The shift from "write code, then prompt" to "write specification, then let the model produce code" is becoming codified ([Addy Osmani 2026][addy-osmani-2026]). Kitsoki's `app.yaml` is exactly a specification-as-prompt: the YAML *is* the LLM's behaviour boundary. An author who writes the YAML produces both the runtime and the spec in one artefact.

### 4.5 Trend 5: The AI productivity paradox

A counter-trend worth naming: a 2026 paper found that AI-assisted programming *decreases* productivity of experienced developers by increasing technical debt and maintenance burden ([arXiv 2510.10165][ai-productivity-paradox]). The plausible mechanism is unbounded LLM latitude producing low-quality output that experienced developers then have to clean up. Kitsoki's architectural answer — *the LLM can only call declared intents with validated slots* — is a direct mitigation: there is nothing to clean up afterward because the LLM never produces uncontrolled output.

---

## 5. Domain Expert Validation

### 5.1 Where the domain experts' mental model meets kitsoki's design

The strongest validation of a tool's fit is when its terminology *is* the domain expert's terminology. Kitsoki's vocabulary, mapped to domain-expert mental models:

| kitsoki term | Domain expert says | Source/parallel |
|---|---|---|
| "Intent alphabet" | "The verbs the LLM is allowed to call" | Inform 7 grammar tokens ([prior-art §1][prior-art]); Rasa intents |
| "State / room" | "Where we are in the workflow" | XState states; Dialogflow CX pages |
| "Slot" | "What we still need to know before we can act" | Rasa forms `required_slots`; CX page parameters |
| "Guard" | "What has to be true before this transition fires" | XState guards; SCXML cond |
| "World" | "Persistent facts about this session" | Workflow context object (LangGraph, Temporal) |
| "Recording" | "The event log we replay in CI" | Temporal history replay; LangGraph checkpointer |
| "Harness" | "Which LLM backend we're using this turn" | LiteLLM-style backend selection |

Every term has a precedent in either the dialogue-manager tradition or the workflow-engine tradition. None of them are kitsoki-invented jargon. This matters for adoption: a Rasa migrator and a Temporal migrator both find familiar nouns when reading kitsoki's docs.

### 5.2 Where kitsoki deliberately departs from the domain default

Three departures worth naming because they are deliberate, not omissions:

1. **No ML training data.** Rasa, Dialogflow, and adjacent tools assume the author supplies example utterances per intent. Kitsoki does not — the LLM is the recognizer and the declared intent alphabet is the spec ([prior-art §3][prior-art]). This is a domain-expert ergonomics bet: declaration-first, not example-first.
2. **No multi-step LLM planning.** LangGraph and CrewAI invite the LLM to plan a chain of tool calls. Kitsoki does one-shot extraction: free text → intent. If multi-step is needed, the *graph* models it, not the LLM ([prior-art §4][prior-art]).
3. **Single generic MCP tool, not per-intent typed tools.** A deliberate choice to keep the tool list stable, the prompt cache warm, and the LLM's prompt decoupled from app-internal intent names ([prior-art §5][prior-art]).

Each departure is documented and justified. For a domain-expert auditor or migrator, this is the right shape of *opinion* — strong, narrow, and grounded.

---

## 6. Domain-Specific Strategic Implications

### 6.1 Where the domain pulls kitsoki forward

- **Audit-trail-as-bug-file is novel in this domain.** Most engineering tools treat audit logs as a side-effect; kitsoki treats the artifact (bug file, ticket) *as* the log. This is a small but real innovation that maps directly onto regulated-industry requirements.
- **Multi-surface session continuity is unserved.** No major dialogue-manager or workflow-engine ships this. Domain experts are working around the gap manually. Kitsoki's transport abstraction is the answer they're already approximating with scripts.
- **Token-cost architectural control.** The four-tier routing stack is a finance-language story for the engineering leader who is being asked to justify LLM spend per developer.

### 6.2 Where the domain pushes back

- **OSS-without-support-contract is a known blocker for regulated buyers.** Rasa solved this with paid tiers; kitsoki has not yet. If kitsoki stays OSS-only, the regulated buyer's procurement team will struggle to onboard it.
- **The dogfood story is credible but small.** Acronis-internal use case is a strong proof, but published case studies in adjacent regulated verticals would compound credibility fast.
- **YAML-as-spec is loved by some, hated by others.** Domain experts coming from a YAML-heavy background (Kubernetes, CI/CD, GitHub Actions) accept it instantly. Domain experts coming from a Python/TypeScript-heavy background may push back on the DSL shape. This is a known acceptance curve, not a unique kitsoki problem.

### 6.3 Domain-grounded value proposition (one paragraph)

> *Kitsoki is the conversational workflow engine for regulated software engineering teams using AI assistants. It encodes the human-guided coding-agent pipeline as a YAML state machine, runs the same machine across local terminal, Jira tickets, and Bitbucket PR comments, and writes every checkpoint into the bug file as a permanent audit record. Mode-2 flow tests replay the whole workflow in CI without an LLM call. The LLM is used only to translate free text into an author-declared intent — never to invent flags, never to step outside the graph.*

---

## 7. Executive Summary

**The domain.** LLM-assisted software-engineering workflows that have to pass an audit. Specifically: ticket-driven bug-fix and feature pipelines that span local terminal, ticket-tracker, and PR-comment surfaces, with the artifact (the ticket itself) doubling as the audit record. Distinct from customer-service chat, code-generation assistants, and generic agent frameworks.

**The practitioner.** Staff-grade engineer in a regulated vertical (financial services, healthcare, government, cybersecurity / data protection) who is on the hook for the team's AI-workflow compliance posture. Has 8–15 years of experience, has personally shipped a compliance-audited system, and prefers OSS to SaaS.

**The pain.** The practitioner cannot govern AI usage without audit trails; pure-agentic frameworks cannot reproduce decisions under audit replay; AI-assisted programming has been shown to *reduce* experienced-developer productivity when LLM latitude is unbounded; per-developer LLM cost control is becoming a board-level concern. All four pains map onto kitsoki design choices that already ship.

**The validation.** Every kitsoki concept (intent, slot, guard, world, recording, harness) has a precedent in either the dialogue-manager tradition or the workflow-engine tradition. Where kitsoki departs from domain defaults (no training data, no LLM planner, single generic tool), the departure is documented and architecturally justified.

**The opportunity.** Three concrete domain-grounded differentiators that no major competitor offers today: audit-trail-as-bug-file, single-state-machine-across-surfaces, four-tier semantic-routing stack with measured ~78% LLM-avoidance.

**The risk.** OSS-without-support-contract blocks regulated procurement; PoC stage limits case-study breadth; YAML-as-spec is a known acquisition-curve cost for non-DSL-fluent practitioners. None of these are architectural — all are go-to-market and credibility curve.

---

## 8. Sources

### AI-assisted engineering workflows
- [Addy Osmani — My LLM Coding Workflow Going into 2026][addy-osmani-2026]
- [Sobonix — AI-Assisted Software Engineering in 2026: The Rise of Human-Guided Coding Agents][sobonix-2026]
- [Cortex — The Engineering Leader's Guide to AI Tools for Developers in 2026][cortex-2026]
- [JetBrains AI Blog — The Best AI Models for Coding: Accuracy, Integration, and Developer Fit (2026)][jetbrains-2026]

### Compliance, governance, audit
- [Glean — Top 7 Industries with Stringent AI Compliance Needs in 2026][glean-compliance-2026]
- [Custodia / DEV — Implementing Visual Audit Trails for LLM Agents in Production (2026)][custodia-audit-trail-2026]
- [Datawiza — Per-Developer LLM API Usage Controls for Engineering Teams][datawiza-2026]
- [Galileo — 6 Best LLM Monitoring Solutions for Enterprise in 2026][galileo-monitoring]

### Domain academic and trend material
- [arXiv 2510.10165 — AI-Assisted Programming Decreases the Productivity of Experienced Developers...][ai-productivity-paradox]
- [Akka — Agentic AI Frameworks for Enterprise Scale: A 2026 Guide][akka-frameworks]

### Internal kitsoki references
- [`README.md` — top-level kitsoki overview, dogfood mode][readme]
- [`docs/architecture/prior-art.md` — Inform/TADS/XState/Rasa/LangGraph genealogy][prior-art]
- [`docs/architecture/transports.md` — multi-surface session abstraction][transports]

[addy-osmani-2026]: https://addyosmani.com/blog/ai-coding-workflow/
[sobonix-2026]: https://www.sobonix.com/blog/ai-assisted-software-engineering-in-2026-the-rise-of-human-guided-coding-agents/
[cortex-2026]: https://www.cortex.io/post/the-engineering-leaders-guide-to-ai-tools-for-developers-in-2026
[jetbrains-2026]: https://blog.jetbrains.com/ai/2026/02/the-best-ai-models-for-coding-accuracy-integration-and-developer-fit/
[glean-compliance-2026]: https://www.glean.com/perspectives/top-7-industries-with-stringent-ai-compliance-needs-in-2026
[custodia-audit-trail-2026]: https://dev.to/custodiaadmin/implementing-visual-audit-trails-for-llm-agents-in-production-a-step-by-step-guide-3p83
[datawiza-2026]: https://www.datawiza.com/blog/industry/per-developer-llm-api-usage-controls-for-engineering-teams/
[galileo-monitoring]: https://galileo.ai/blog/best-llm-monitoring-solutions-enterprise
[ai-productivity-paradox]: https://arxiv.org/pdf/2510.10165
[akka-frameworks]: https://akka.io/blog/agentic-ai-frameworks
[readme]: ../../README.md
[prior-art]: ../prior-art.md
[transports]: ../transports.md

---

**Domain Research Completion Date:** 2026-05-19
**Source Verification:** All practitioner-pain claims and trend claims cited inline with 2026 sources.
**Domain Confidence Level:** High for architectural and workflow claims; medium for practitioner-persona detail (composite from published sources, not primary interviews at this stage).
