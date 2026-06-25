# Epic: Generic feedback SDK and Slidey reports

**Status:** Draft v1. No slices implemented yet.
**Kind:**   epic
**Slices:** 6 (0/6 shipped)

## Why

Kitsoki has good local ingredients for bug reporting: the web surface already
captures rrweb replay, scrubbed network evidence, console/error state, and
operator prose behind a review-before-submit modal; the spatial oracle can turn
a point or box into selectors, roles, text, and bounding boxes over live DOM or
rrweb replay; Slidey can carry replayable media in a durable artifact. Those
pieces are still Kitsoki-specific and developer-local. We need a framework
neutral JavaScript reporter that any site can embed to collect bugs, feature
requests, copy/tutorial feedback, translation feedback, demo feedback, and docs
feedback, then produce a reviewed narrative report that a human, an LLM, and a
GitHub-backed agent can all consume.

The privacy bar is higher than "scrub the report before filing." Session replay
can leak personal data through rendered text, behavior, reflected state, network
traffic, console values, and stable identifiers even when form inputs are
masked. The default product posture should be data avoidance: collect no
personal data unless a useful report cannot be produced without it, keep raw
draft data browser-local by default, and send only reviewed/redacted artifacts
to the sink.

## What changes

Add a framework-neutral `@kitsoki/feedback` browser SDK with:

- a chromeless reporter UI that can be mounted with a default floating trigger
  or fully replaced by the host application;
- a plugin API for capture, app context, privacy policy, review transforms, and
  narrative generation;
- privacy-first capture primitives for rrweb replay, screenshots, spatial
  anchors, console/error state, network summaries, route/docs/demo/i18n context,
  and explicit user input;
- a mandatory privacy manifest that records source, sensitivity,
  identifiability, policy, legal basis, retention, review state, and provenance
  for every contributed field;
- a reviewed Slidey deck artifact that tells the report as a coherent story:
  summary, precursor state, replay, spatial anchor, redacted evidence, privacy
  review, LLM review, and backend receipt;
- a generic `ReportSink` contract that the parallel GitHub agent can implement
  without the frontend knowing issue labels, upload mechanics, auth, or storage
  layout.

The SDK should support typed report kinds (`bug`, `feature_request`,
`copy_feedback`, `translation_feedback`, `demo_feedback`, `docs_feedback`,
`question`) while keeping the capture pipeline generic. The kind changes prompt
copy, required fields, deck template, and sink routing metadata; it should not
fork the evidence model.

## Impact

- **Spans:** tracing / web UI / media / product site / backend contract.
- **Net surface:** new browser SDK package, extracted capture/privacy modules
  from `tools/runstatus`, shared spatial picker, Slidey deck generator, product
  site plugin context, and a generic report sink adapter.
- **Docs on ship:** `docs/architecture/` for the report/sink/privacy contracts,
  `docs/tui/` for the reused bug-report/spatial flows, `docs/media/` for Slidey
  report artifacts, and product-site docs for public feedback deployment.
- **Current anchors:** web bug reporting is documented in
  `docs/tui/web-ui.md`; spatial capture and chromeless point handoff are
  documented in `docs/tui/spatial-capture.md` and
  `docs/tui/spatial-handoff.md`; the current rrweb recorder lives in
  `tools/runstatus/src/data/session-capture.ts`; the current report state lives
  in `tools/runstatus/src/stores/bugReport.ts`.

## Existing substrate

- Web bug reporting already has the right review-before-submit model: the Meta
  menu captures rrweb, scrubbed HAR, console/error state, and optional operator
  prose before filing (`docs/tui/web-ui.md`).
- The rrweb recorder keeps a bounded rolling buffer, preserves checkpoint
  metadata needed for replay after trimming, lazy-loads rrweb, and is injectable
  for no-real-browser tests
  (`tools/runstatus/src/data/session-capture.ts`).
- Current rrweb capture masks form values and text by default and blocks
  password fields, but privacy must become a typed product contract rather than
  a capture option.
- The store already separates capture from filing, previews backend evidence,
  and submits only after review (`tools/runstatus/src/stores/bugReport.ts`).
- Spatial capture resolves point/box/element intent into selectors, roles,
  text, and bounding boxes over either live DOM or rrweb replay
  (`docs/tui/spatial-capture.md`).
- The existing chromeless `/point` handoff gives the right UI shape: a
  token-scoped page with one interaction and no surrounding app chrome
  (`docs/tui/spatial-handoff.md`).

## Market and research context

Most commercial tools solve pieces of this:

- Sentry Session Replay provides replay with framework/browser SDK setup,
  console and network context, sampling, and privacy controls such as
  `maskAllText` and `blockAllMedia`.
  Source: <https://sentry.io/product/session-replay/>
- Userback sells visual bug reporting with annotated screenshots, video,
  console logs, session replay, and customer attributes.
  Source: <https://userback.io/>
- Marker.io focuses on website feedback/UAT with screenshots, annotations,
  technical metadata, and tracker integrations.
  Source: <https://marker.io/>
- Fullstory is broader behavioral analytics/session replay with AI summaries
  and privacy controls.
  Source: <https://www.fullstory.com/platform/session-replay/>
- OpenReplay is the strongest open/self-hosted comparator: session replay,
  errors, network activity, DevTools, analytics, and self-hosting as the privacy
  and compliance story.
  Source: <https://openreplay.com/>

The gap is the artifact and review shape. Existing tools collect evidence and
route tickets; they do not generally produce a portable, human-reviewed Slidey
deck with rrweb playback, spatial anchors, precursor state, privacy manifest,
and LLM-authored narrative that can be consumed by a code agent.

The warning sign is that replay privacy cannot rely on input masking alone.
Research revisiting Hotjar found that input allowlisting improved privacy, but
non-input page content can still expose behavior and reflected personal data.
Source: <https://arxiv.org/abs/2309.11253>

## GDPR and privacy posture

The SDK must be privacy-oriented by construction. GDPR treats personal data as
any information relating to an identified or identifiable natural person,
including indirect identifiers and online identifiers
(<https://gdpr-info.eu/art-4-gdpr/>). URLs, account ids, screenshots, replay DOM
text, console values, network headers, locale, and behavior sequences can all
become personal data when combined.

Design requirements:

- **Data avoidance first.** Prefer route ids, feature ids, component ids,
  message keys, source string hashes, DOM roles, selectors, and bounding boxes
  over raw rendered text, screenshots, network bodies, or console objects.
- **Purpose limitation and minimisation.** Collect only what is necessary for
  the specific feedback purpose
  (<https://gdpr-info.eu/art-5-gdpr/>).
- **Host-declared lawful basis.** If a bundle contains personal data, the host
  must record a lawful basis such as consent or legitimate interests; the SDK
  cannot invent that basis (<https://gdpr-info.eu/art-6-gdpr/>).
- **Privacy by design and default.** Defaults must limit amount collected,
  processing extent, storage period, and accessibility
  (<https://gdpr-info.eu/art-25-gdpr/>; EDPB Guidelines 4/2019:
  <https://www.edpb.europa.eu/sites/default/files/files/file1/edpb_guidelines_201904_dataprotection_by_design_and_by_default_v2.0_en.pdf>).
- **Pseudonymisation is not anonymisation.** Pseudonymised data remains personal
  data when re-identification is possible with additional information
  (<https://gdpr-info.eu/recitals/no-26/>; ICO guidance:
  <https://ico.org.uk/for-organisations/uk-gdpr-guidance-and-resources/data-sharing/anonymisation/pseudonymisation/>).
- **DPIA awareness.** Broad replay, systematic behavior monitoring, special
  category data, public-site tracking, or LLM review of user behavior may
  require a DPIA before rollout (<https://gdpr-info.eu/art-35-gdpr/>).
- **Reviewed-only remote processing.** The LLM reviewer and sink receive only
  the post-policy bundle. Raw drafts stay browser-local by default.

For this proposal, "GDPR compliant" means the SDK gives the host the controls,
records, and safe defaults needed to comply. The host remains the controller for
notices, lawful basis, data-subject rights, retention policy, processor
agreements, and transfer choices.

## Privacy model

Every captured field carries privacy metadata:

```ts
interface PrivacyFieldPolicy {
  path: string;
  source:
    | "rrweb"
    | "console"
    | "network"
    | "dom"
    | "plugin"
    | "user_text"
    | "spatial"
    | "host_context"
    | "derived_summary";
  sensitivity: "public" | "internal" | "personal" | "special_category" | "secret" | "unknown";
  identifiability: "none" | "direct" | "indirect" | "linkable" | "aggregate";
  policy:
    | "include"
    | "mask"
    | "drop"
    | "hash"
    | "hmac"
    | "synthetic_swap"
    | "alias_per_report"
    | "review_required";
  legalBasis:
    | "none"
    | "consent"
    | "legitimate_interest"
    | "contract"
    | "legal_obligation"
    | "not_personal_data"
    | "host_declared";
  reviewState:
    | "unreviewed"
    | "user_approved"
    | "auto_redacted"
    | "owner_approved"
    | "removed"
    | "swapped";
  retention: "memory_only" | "draft_ttl" | "report_ttl" | "sink_policy";
  provenance: {
    providerId: string;
    rulesetVersion: string;
    ruleId: string;
    transformedAt: string;
  };
}
```

Default behavior:

- Mask or drop inputs, text nodes, cookies, auth headers, local paths, emails,
  phone-like values, access tokens, IP addresses, device ids, and
  customer-specific ids before data leaves the browser.
- Capture network metadata by default; request/response bodies require
  host-owned allowlists and still pass through schema-aware redaction.
- Capture console entries as level, timestamp, message template, repo-relative
  stack frames, and redacted argument summaries. Full object capture is opt-in.
- Treat stable hashes as linkable personal data when the original value is a
  person, account, device, IP, project, or organization identifier. Prefer
  per-report aliases unless cross-report correlation is explicitly needed.
- Block submit when a plugin emits undeclared fields, `unknown` sensitivity, or
  high-risk fields without a host-declared policy.
- Give the reporter user controls to remove, reveal, or synthetic-swap
  sensitive fields before submit.
- Record the privacy manifest in the final bundle and in the Slidey deck's PII
  review scene.

## Capture bundle

The sink receives one reviewed bundle:

```ts
interface FeedbackReportBundle {
  schema: "kitsoki.feedback.report.v1";
  idempotencyKey: string;
  app: string;
  url: string;
  title?: string;
  kind: string;
  userText: string;
  createdAt: string;
  privacyVersion: string;
  noticeVersion?: string;
  legalBasis?: "none" | "consent" | "legitimate_interest" | "host_declared";
  viewport: { width: number; height: number; devicePixelRatio: number };
  privacyManifest: PrivacyManifest;
  evidence: {
    rrweb?: { events: unknown[]; durationMs: number };
    spatial?: SpatialAnchor[];
    console?: ConsoleEntry[];
    network?: NetworkSummary[];
    screenshots?: EvidenceImage[];
    precursorState?: Record<string, unknown>;
    hostContext?: Record<string, unknown>;
  };
  narrative?: {
    slideySpec?: unknown;
    llmReview?: LlmReviewResult;
    summaryMarkdown?: string;
  };
}
```

For the Kitsoki product site, precursor state should include current route,
feature/demo/tutorial/docs page, current tour step, selected locale, feature id,
Slidey deck/scene id where applicable, and visible product-site context that
makes the feedback actionable.

## Plugin API

Plugins contribute context and evidence, but each contribution must declare its
privacy policy:

```ts
interface FeedbackPlugin {
  id: string;
  setup?(api: FeedbackPluginApi): void | (() => void);
  context?(ctx: CaptureContext): Promise<ContextContribution> | ContextContribution;
  evidence?(ctx: CaptureContext): Promise<EvidenceContribution[]> | EvidenceContribution[];
  beforeReview?(bundle: DraftBundle): Promise<DraftBundle> | DraftBundle;
  beforeSubmit?(bundle: ReviewedBundle): Promise<ReviewedBundle> | ReviewedBundle;
}
```

First-party plugins:

- `rrwebRecorder`: bounded rolling DOM replay with configurable
  text/input/media/canvas policy.
- `spatialPicker`: point/box/element anchor capture over live DOM or replay.
- `consoleCapture`: bounded console ring with scrub rules.
- `networkCapture`: fetch/XHR metadata capture; body capture only through
  allowlisted host policy.
- `routeContext`: URL, route name, params, search, referrer.
- `productSiteContext`: feature id, demo id, docs page, tutorial step, selected
  tab, current Slidey scene.
- `i18nContext`: locale, message key, source string hash, rendered string, and
  translation provider metadata when available.
- `deckNarrative`: converts the reviewed bundle into a Slidey report deck.

## Slidey report deck

The deck is the report artifact, not just an attachment. Minimum scenes:

1. Summary: report kind, user statement, affected product area, and LLM
   distilled problem.
2. Precursor state: route, feature/demo/doc/tutorial step, locale, app version,
   and relevant plugin context.
3. Replay: rrweb playback segment covering the lead-up, with a marker at the
   report moment.
4. Spatial anchor: clicked/boxed region, selector/role/text/bbox, and user
   complaint.
5. Evidence: console/network/errors as compact redacted tables.
6. Privacy review: what was removed, masked, swapped, retained, or blocked.
7. LLM review: coherent narrative, suspected category, likely owner/component,
   reproduction hints, missing context, and confidence.
8. Backend receipt: sink id/URL/status and artifact inventory.

Automated deck tests use a cassette/stubbed reviewer. Default automated tests
must never call a real LLM or incur model cost.

## Generic report sink

The frontend SDK depends only on a generic sink:

```ts
interface ReportSink {
  preview?(bundle: DraftBundle): Promise<PreviewResult>;
  submit(bundle: ReviewedBundle): Promise<SubmitResult>;
  erase?(externalId: string, reason: ErasureReason): Promise<void>;
  replaceEvidence?(externalId: string, bundle: ReviewedBundle): Promise<SubmitResult>;
}

interface SubmitResult {
  externalId: string;
  url?: string;
  status: "created" | "queued" | "updated";
  artifactUrls?: Record<string, string>;
}
```

The parallel GitHub agent needs to accept a reviewed bundle, store or link the
Slidey deck, rrweb JSON, redacted evidence JSON, and privacy manifest, preserve
idempotency, return a stable URL/id, and support erasure, retention expiry, and
evidence replacement. It should store only reviewed/redacted artifacts by
default. If any raw draft artifact is temporarily retained, it needs a separate
TTL and must not be referenced from the report deck or public issue body.

## Product-site integration

The product site should use the SDK for:

- tutorial wording feedback;
- translation feedback;
- demo/tutorial mismatch reports;
- feature requests tied to the workflow being viewed;
- product-site bugs.

The default reporter can live globally, but docs/demo/tutorial pages should
also render contextual actions like "Comment on this step" or "Report
translation" that call `reporter.open({ kind, context })`.

## Slices

| # | Slice | Kind | Scope (one line) | Depends on | Status | File |
|---|---|---|---|---|---|---|
| 1 | Capture core extraction | tracing | Extract rrweb, console/error, network summary, screenshot, and bundle assembly from runstatus into a framework-neutral browser core. | - | Draft | Child proposal deferred |
| 2 | Privacy policy engine | runtime | Add privacy manifest types, plugin preflight, redaction/swap/alias transforms, retention metadata, and review blockers. | 1 | Draft | Child proposal deferred |
| 3 | Chromeless reporter UI | tui | Build the default floating trigger/reporter and custom trigger/custom panel integration points without Kitsoki app chrome. | 1, 2 | Draft | Child proposal deferred |
| 4 | Spatial feedback plugin | tracing | Promote live DOM / rrweb replay point-box-element resolution into an SDK plugin. | 1, 2 | Draft | Child proposal deferred |
| 5 | Slidey report generator | tracing | Generate a reviewed Slidey deck with replay, precursor state, spatial anchor, evidence, privacy review, and stubbed LLM narrative. | 1, 2, 4 | Draft | Child proposal deferred |
| 6 | Product-site and sink integration | runtime | Wire Kitsoki product-site context plugins and the generic sink adapter for the parallel GitHub agent. | 1-5 | Draft | Child proposal deferred |

## Sequencing

```text
#1 capture core
   +--> #2 privacy engine --> #3 chromeless UI
   |                         +--> #4 spatial plugin --> #5 Slidey generator
   +--------------------------------------------------> #6 site + sink
```

Privacy lands before any public product-site deployment. The Slidey generator
can start once reviewed bundles and spatial anchors are stable. The GitHub-agent
adapter should stay thin until the parallel backend API is ready.

## Shared decisions

1. **Reviewed bundle is the boundary.** Remote LLM review and sink submission
   receive only the post-policy bundle. Raw drafts stay browser-local by
   default.
2. **Structural evidence before content.** Route ids, feature ids, message
   keys, selectors, roles, and bounding boxes are preferred over screenshots,
   rendered text, console objects, or network bodies.
3. **Plugin output is fail-closed.** Undeclared privacy policy or `unknown`
   sensitivity blocks submit until the user or host reviews it.
4. **Pseudonymised is still personal unless proven otherwise.** Stable hashes
   and HMACs are treated as linkable unless the host records why they are not.
5. **The deck is a durable artifact.** The issue/request body can summarize and
   link the deck, but the deck is the coherent review object.
6. **Tests stay no-LLM.** LLM review is dependency-injected and cassette/stubbed
   in automated tests.

## Cross-cutting open questions

1. Should generic sites collect network metadata by default, or should all
   network capture require explicit host opt-in? *Lean: metadata only by
   default, no bodies.*
2. Does the product-site deployment need consent before the in-memory rolling
   replay buffer starts, or is explicit user-open plus masked memory-only
   capture enough under the site owner's lawful basis? *Lean: masked
   memory-only buffer starts only after the user opens the reporter for public
   site v1.*
3. Should deck generation happen browser-side for preview and server-side for
   durable artifacts, or should the browser only submit the reviewed bundle?
   *Lean: browser preview, sink/backend durable rendering.*
4. What is the minimum no-auth abuse control for public feedback? *Lean:
   sink-edge rate limits/captcha/proof checks rather than persistent reporter
   identity.*
5. Should reports expose "private to maintainers" vs "public issue" to the
   reporter, or should that be sink policy? *Lean: sink policy for v1.*

## Tasks

- [ ] 1. Split this epic into focused child proposals once the slice boundaries
      above are accepted.
- [ ] 2. Implement the capture-core slice with dependency injection and no-LLM /
      no-real-browser tests.
- [ ] 3. Implement privacy preflight, field policies, redaction/swap/alias
      transforms, retention metadata, and review blockers.
- [ ] 4. Build the chromeless reporter UI and framework wrapper examples.
- [ ] 5. Promote spatial picker/element resolver into the SDK plugin layer.
- [ ] 6. Add Slidey report generation with stubbed LLM review fixtures.
- [ ] 7. Define the mock sink and thin GitHub-agent adapter once that API is
      stable.
- [ ] 8. Wire product-site context plugins behind a feature flag.
- [ ] 9. Migrate shipped behavior into narrative docs and trim/delete this
      proposal as slices land.

## Non-goals

- Build the GitHub storage agent in this proposal. This epic defines the
  frontend-facing sink contract and required backend capabilities only.
- Turn feedback reporting into general analytics or heatmaps.
- Store raw replay, screenshots, HARs, or console objects by default.
- Send raw draft data to a remote LLM.
- Replace Sentry/OpenReplay-style observability for application telemetry. This
  is a user-initiated feedback/report artifact pipeline.
