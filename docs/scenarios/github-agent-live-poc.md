# Live GitHub Agent POC Scenario

**Status:** Active scenario.
**Owner docs:** [`docs/proposals/kitsoki-github-agent.md`](../proposals/kitsoki-github-agent.md), [`docs/architecture/github-app-setup.md`](../architecture/github-app-setup.md)
**Primary command:** `scripts/run-gh-agent-live-poc.sh`
**Primary artifact:** `.artifacts/github-agent-live/live-github-agent.slidey.json`

This scenario is the controlled definition of the live `@kitsoki` GitHub-agent
proof of concept. It exists so the POC flow can be updated deliberately instead
of being reconstructed from transient `.context` notes.

## Intent

Prove that a live GitHub App mention can enter kitsoki, dispatch to the expected
story route, create a public run link, post the App response back to GitHub, and
produce a reviewable Slidey deck from live evidence.

The scenario proves the live GitHub front door and evidence pipeline. It does
not claim full autonomous PR delivery, production authorization, or merge
authority.

## Actors And Systems

- **Requester:** the GitHub user who creates the test issues or PR comment.
- **GitHub App:** the installed `@kitsoki` App on the throwaway test repo.
- **Hosted agent:** `kitsoki-gh-agent.service` on the test VM.
- **Kitsoki stories:** the no-LLM dispatch routes used by the POC.
- **Run viewer:** `https://kitsoki-test.slothattax.me/run/<job-id>`.
- **Deck:** a Slidey source deck embedding rrweb logs.

## Preconditions

- The working branch is the branch under test.
- The GitHub App is installed on the selected throwaway repo.
- The VM service can be deployed from the current checkout.
- A throwaway pull request URL is available for the PR-status case.
- Live GitHub and VM mutation approval has been given for the run.
- Developer-arc media is available as rrweb whenever possible.
- Automated tests and routine verification use cassettes or deterministic
  checks; real LLM calls are not part of this scenario.

## Cases

The approved live POC contains four cases:

| Case | GitHub trigger | Expected story route | Expected proof state |
|---|---|---|---|
| `bug-issue` | `@kitsoki` on a bug-labelled issue | bug route | App comment with run link and completed proof |
| `feature-issue` | `@kitsoki` on a feature-labelled issue | feature/dev-story route | App comment with run link and completed proof |
| `guidance` | ambiguous `@kitsoki` issue | guidance route | App comment asks for direction and job parks at `awaiting_guidance` |
| `pr-status` | `@kitsoki` on a throwaway PR | PR status route | App comment with run link and PR-status proof |

## Flow

1. Run `scripts/run-gh-agent-live-poc.sh --pr-url <throwaway-pr-url>` as a dry
   run and review the printed VM and GitHub mutations.
2. Run the approved live command with `--yes-live-mutations`, the same PR URL,
   `--capture`, and `--developer-arc-media <rrweb-or-compatible-media>`.
3. The runner deploys the current checkout to the VM unless `--skip-deploy` is
   explicitly set.
4. The runner creates the bug, feature, and guidance issues and comments on the
   supplied PR.
5. The GitHub App webhook reaches the hosted agent and creates one job per
   case.
6. Each job writes a run URL and App comment id before evidence is collected.
7. The runner writes per-case evidence notes under `.context/live-poc-*.md` and
   a run summary under `.context/live-poc-run-<stamp>.md`.
8. The runner builds Playwright capture plans under
   `.artifacts/github-agent-live/capture-plan-<case>.json`.
9. The capture harness records tour-driven rrweb clips for the live GitHub
   thread, App comment, run page, and run API evidence for each case. Each clip
   must show what the reviewer would do or inspect on that surface: narrated
   highlights, visible scroll or focus movement, and the handoff from GitHub to
   the run link.
10. The deck builder creates
    `.artifacts/github-agent-live/live-github-agent.slidey.json`.
11. The verifier checks the evidence bundle and writes the JSON report requested
    by the caller, normally under `.context`.

## Artifact Contract

The primary deliverable is the Slidey source deck:

```
.artifacts/github-agent-live/live-github-agent.slidey.json
```

The deck must embed rrweb logs for captured acts. It must not embed MP4 clips
for surfaces rrweb can represent. MP4 output is an explicit optional export for
review or sharing, not a default scenario artifact. The HTML viewer is also not
a default scenario artifact.

Expected supporting artifacts:

```
.context/live-poc-<case>.md
.context/live-poc-run-<stamp>.md
.context/live-github-poc-verifier-report.json
.artifacts/github-agent-live/capture-plan-<case>.json
.artifacts/github-agent-live/<case>/*.rrweb.json
```

## Acceptance Gates

A run is acceptable only when:

- The four case evidence notes exist and point at live GitHub URLs.
- Every case has a VM job row with `run_url` and `comment_id`.
- The guidance case parks at `awaiting_guidance`.
- Capture plans point at the App response and run URL, not only at the
  requester mention.
- The Slidey deck exists at the `.slidey.json` path and references the live
  GitHub App on `kitsoki-test`.
- The deck references rrweb source clips for the GitHub cases.
- The rrweb clips are tour-driven evidence, not five-second passive holds on
  disconnected pages.
- `scripts/verify-gh-agent-live-poc.mjs` passes without requiring an HTML viewer
  or MP4 export.

## Non-Default Exports

Only produce these artifacts when explicitly requested:

- `.artifacts/github-agent-live/live-github-agent.mp4`
- `.artifacts/github-agent-live/live-github-agent.mp4.chapters.json`
- `.artifacts/github-agent-live/live-github-agent.html`

The default path should remain fast and source-first: live evidence plus the
Slidey deck.

## Change Control

When the live POC flow changes:

1. Update this scenario first.
2. Update `docs/architecture/github-app-setup.md` if operator commands changed.
3. Update `scripts/run-gh-agent-live-poc.sh` and
   `scripts/verify-gh-agent-live-poc.mjs` to enforce the new contract.
4. Update `.agents/skills/kitsoki-ui-demo/SKILL.md` or
   `.agents/skills/kitsoki-ui-qa/SKILL.md` if the mistake would otherwise recur.
5. Append transient run details to `.context/github-agent-poc-work-log.md`.
