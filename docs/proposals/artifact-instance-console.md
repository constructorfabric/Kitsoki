# TUI/Web: artifact job console

**Status:** Draft v2. Re-scoped from an instance-only console to the unified
artifact-job console for durable runs, workspaces, sharing, and cleanup. Nothing
implemented yet.
**Kind:**   tui
**Epic:**   ../artifact-driven-stories.md
**Depends on:** [`artifact-job-registry.md`](artifact-job-registry.md),
[`trace-artifact-service.md`](trace-artifact-service.md),
[`artifact-instances.md`](artifact-instances.md),
[`artifact-publish-lifecycle.md`](artifact-publish-lifecycle.md), and
[`dev-story-artifact-jobs.md`](dev-story-artifact-jobs.md)

## Why

The runtime slices can persist jobs, traces, artifacts, and workspaces, but an
operator still needs a visible product surface:

- "What jobs are still running or awaiting me?"
- "Can I resume the design I started yesterday?"
- "Where is the run URL and artifact gallery?"
- "Can I share this draft workspace with someone else?"
- "Which archived workspaces are old or huge enough to clean up?"

Current surfaces show fragments: `/work` shows active operation handles, the web
Home shows live sessions, and workspace-manager is a dev-story stub. This slice
turns those fragments into one artifact-job console across TUI and web.

## What changes

Add a data-driven console backed by artifact-job registry, run/artifact index,
and instance lifecycle calls.

It appears in three places:

- TUI `/work` gains persistent artifact-job rows, not just current in-session
  operations.
- Web Home lists artifact jobs across sessions with resume/open/share actions.
- In-session dev-story views can open the scoped instance manager for the
  current job.

The console supports:

- list/filter by `running`, `awaiting_input`, `interrupted`, `done`, `failed`,
  `archived`;
- resume/attach to a job;
- open stable run URL;
- browse emitted artifacts;
- open workspace files;
- promote draft to shared workspace;
- publish canonical doc/report when the story declares publish policy;
- warn and delete reclaimable archives after guarded confirmation.

## Impact

- **TUI:** extend `/work`, add typed `artifact_job_list` rendering, preserve the
  existing `/work drive`, `/work artifact`, and `/work summary` actions.
- **Web:** Home artifact-job table, in-session job drawer/banner, artifact
  gallery consuming `runstatus.run.artifacts`, and share/publish/archive
  controls when policy allows them.
- **Rendering:** typed elements and Vue components only; no hand-built table
  strings for the new list.
- **Docs on ship:** `docs/tui/web-ui.md` and
  `docs/stories/artifact-driven-stories.md`.

## Rendering changes

`artifact_job_list` rows:

| Field | Notes |
|---|---|
| `job_id` / `title` | stable identity and display label |
| `status` / `phase` | badge + compact phase |
| `updated_at` / `age` | sorting and stale warnings |
| `run_url` | open action |
| `terminal_artifact_handle` | artifact action |
| `workspace_instance_id` | workspace/share/publish actions |
| `size_bytes` / `retention` | archive warning |
| `actions` | resume, open, share, publish, delete when allowed |

The web artifact gallery reuses the existing by-handle media rendering path so
chat-bubble media and gallery media render from one MIME switch.

## Input & commands

| Command / control | Does |
|---|---|
| `/work` | list artifact jobs and current operations |
| `/work resume <job_id>` | attach to a persisted job |
| `/work open <job_id>` | open the run URL |
| `/work artifact <job_id>` | open the terminal artifact or artifact gallery |
| `/work share <job_id>` | promote the draft workspace when policy allows |
| `/work publish <job_id>` | publish the canonical doc/report when policy allows |
| `/work delete <job_id>` | guarded reclaim for archived instances only |

Web exposes the same actions as buttons/menus, authorized by the same registry
and lifecycle policy.

## Tests

- TUI golden for `artifact_job_list` with mixed states, terminal artifacts, and
  stale archive warning.
- TUI captured-IO test for guarded delete: host call, slog output, and re-render
  do not corrupt the frame.
- Web unit tests for Home job rows and in-session artifact-job drawer.
- Playwright replay test for a stored run artifact gallery: image/video/PDF/deck
  cards resolve by handle and the video poster route works.
- Negative tests: share/publish/delete actions are absent or disabled when the
  job lacks the required lifecycle policy.

## Tasks

```
## 1. Render
- [ ] 1.1 `artifact_job_list` typed element + TUI renderer
- [ ] 1.2 Web Home artifact-job table and in-session job drawer
- [ ] 1.3 ArtifactGallery consumes `runstatus.run.artifacts`
- [ ] 1.4 Resume/open/share/publish/delete actions render from policy data

## 2. Drive
- [ ] 2.1 TUI `/work resume|open|artifact|share|publish|delete`
- [ ] 2.2 Web RPC actions for the same commands
- [ ] 2.3 Guarded delete confirmation refuses non-archived jobs without force
- [ ] 2.4 Share/publish actions call lifecycle host surfaces and refresh the row

## 3. Prove + document
- [ ] 3.1 TUI golden + captured-IO tests
- [ ] 3.2 Web unit + Playwright replay tests
- [ ] 3.3 Manual no-LLM dev-story artifact-job pass with screenshots
- [ ] 3.4 Document the console in docs/tui/web-ui.md and docs/stories/artifact-driven-stories.md
```

## Open questions

1. **Home scope.** Show all artifact jobs by default or only active/recent ones.
   *Lean: active/recent by default, archived behind a filter.*
2. **Artifact gallery in TUI.** Terminal opener only vs a text gallery list.
   *Lean: list handles and open via `/work artifact`; rich preview stays web.*
3. **Share URL shape.** File path, run URL, or both. *Lean: both when available:
   run URL for trace/media, shared workspace path for editable docs.*

## Non-goals

- **No new lifecycle semantics.** Runtime slices own registry, trace storage,
  share/publish, and GC decisions.
- **No GitHub OAuth drive gate.** GitHub hosted-run auth remains in the
  GitHub-agent viewer slice; this console consumes resolved permissions.
- **No real-time co-editing.**
