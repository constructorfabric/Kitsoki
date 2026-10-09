# Story: dev-story artifact jobs

**Status:** Draft v1. Nothing implemented yet.
**Kind:**   story
**Epic:**   ../artifact-driven-stories.md
**Depends on:** [`artifact-job-registry.md`](artifact-job-registry.md),
[`trace-artifact-service.md`](trace-artifact-service.md),
[`artifact-instances.md`](artifact-instances.md), and
[`artifact-publish-lifecycle.md`](artifact-publish-lifecycle.md)

## Why

Dev-story is the worked example for artifact-heavy work: PRDs, design briefs,
proposal drafts, implementation plans, QA reports, videos, screenshots, and
completion-state JSON. It already emits many of those files, but each path owns
its persistence story differently. The result is surprising: useful artifacts
exist on disk, while the job that produced them is hard to reopen, share, or
inspect from another session.

This slice makes dev-story the reference consumer for persistent artifact jobs.

## What changes

Dev-story declares artifact jobs for the long-running flows that naturally
outlive one operator interaction:

- PRD -> design
- design proposal drafting and publish
- implementation/delivery tail
- bugfix/fix-tests handoff paths imported by provider-bound project wrappers
- demo/video and QA gates that produce terminal artifacts

Each dev-story artifact job registers a durable `job_id`, binds its run URL,
uses a keyed workspace instance when it creates editable artifacts, journals
terminal artifacts through `host.artifacts_dir`, and exposes resume/share/publish
actions through the console.

## Impact

- **Story files:** `stories/dev-story/rooms/design*.yaml`,
  `stories/dev-story/scripts/design_workspace.star`,
  `stories/dev-story/scripts/publish_design.py`, imported bugfix/fix-tests
  handoff rooms, and no-LLM flow fixtures.
- **Runtime surface consumed:** artifact-job registry, trace/artifact service,
  `artifacts:` story block, `iface.instance.*`, and lifecycle host calls.
- **Backward compat:** existing cassettes remain valid. Agent-producing phases
  stay cassette-backed; only job/workspace/run registration changes.
- **Docs on ship:** dev-story onboarding and artifact-driven story docs show
  how to author a resumable job with artifacts.

## Story model

```
operator starts dev-story design
  -> register artifact job
  -> resolve workspace instance by key
  -> phases write 001-brief ... 005-proposal.md
  -> run/artifact index records emitted handles

operator leaves
  -> another session lists artifact jobs
  -> resume job
  -> reopen run trace and workspace

operator shares/publishes
  -> promote workspace or publish final doc
  -> disposition/archive policy applies
```

## Tasks

```
## 1. Design path
- [ ] 1.1 Add artifact-job registration to design entry
- [ ] 1.2 Replace `design_workspace.star` mint-only behavior with instance resolve
- [ ] 1.3 Keep numbered artifact writes but bind them to the job/workspace handles
- [ ] 1.4 Replace publish script-only behavior with lifecycle publish/dispose calls

## 2. Delivery paths
- [ ] 2.1 Mark implementation/delivery-tail handoffs as artifact jobs when they produce terminal artifacts
- [ ] 2.2 Bind imported bugfix/fix-tests operation handles to artifact-job rows
- [ ] 2.3 Preserve terminal_artifact_handle and completion-state JSON in the job summary
- [ ] 2.4 Add resume/share/open actions to dev-story views where appropriate

## 3. Prove + document
- [ ] 3.1 No-LLM flow: start design, leave, list job, resume into saved phase
- [ ] 3.2 No-LLM flow: publish design, archive_as_is keeps workspace and run artifacts
- [ ] 3.3 No-LLM flow: imported fix-tests job completes with terminal artifact visible from job list
- [ ] 3.4 Update docs/stories/dev-story-onboarding.md and docs/stories/artifact-driven-stories.md
```

## Open questions

1. **Which dev-story flows register by default.** All long-running flows vs only
   flows with terminal artifacts. *Lean: register any flow that either creates a
   workspace or emits a terminal artifact.*
2. **Job naming.** Operator-provided title vs derived from phase-one artifact.
   *Lean: explicit title when the intake has one; derived slug otherwise.*
3. **Imported child jobs.** Separate child job rows vs folded into parent job
   summary. *Lean: child rows with parent_job_id so the console can show the
   tree without hiding terminal artifacts.*

## Non-goals

- **No new LLM calls.** Existing agent phases remain cassette-backed in tests.
- **No redesign of dev-story rooms.** This is persistence/shareability plumbing,
  not a new PRD/design/delivery flow.
- **No GitHub-specific behavior.** GitHub consumes the same substrate later.
