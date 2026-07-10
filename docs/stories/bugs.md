# Bug reports

> Kitsoki developer bug loops are local by default: web/TUI/Studio and
> `kitsoki bug create --sink local-artifact` write the same markdown ticket
> format under `.artifacts/issues/bugs/`, with evidence beside it under
> `.artifacts`. Remote handoffs can still file GitHub issues via
> `kitsoki bug create --github <owner/repo>` or `kitsoki web --ticket-repo
> <owner/repo>`; see
> [hosts.md → host.gh.ticket](../architecture/hosts.md#hostghticket--github-issues-backed-tracker).
> Kitsoki's committed in-repo `issues/` pile is a frozen archive
> ([`issues/DEPRECATED.md`](../../issues/DEPRECATED.md)); story-owned fixtures
> may still use committed `issues/bugs/` files deliberately.

A bug filed from inside a running app lands as a single markdown file
on disk. There is no service, no database, and no schema beyond what
this doc describes — the pile is grep-friendly, hand-editable, and
survives a `git mv` without losing meaning.

Programmatic/batch filers use the same local-artifact sink and file format
without a human in the loop: `kitsoki bug file-findings` files a
product-journey run bundle's credible findings to GitHub, and
`tools/product-journey/run.py --file-local-findings` (driven by the
`stories/product-journey-qa` `campaign_issue_fix` intent, default
`finding_sink=local-artifact`) files the same findings as local tickets under
`.artifacts/issues/bugs/` instead — see
[`agentic-qa-campaigns.md`](../guide/development/agentic-qa-campaigns.md) for
the campaign finding-sink policy this doc's format backs.

Two filing targets exist:

- **Story bugs** — the surface that surprised you was the *app you
  were playing*. Filed under the running app's directory.
- **Kitsoki bugs** — the surface was the *engine itself* (the TUI,
  the orchestrator, a host handler, …). Filed under `$KITSOKI_REPO`.

Both targets use the same CLI and the same on-disk format; the
distinction is in the trigger you pick when filing.

---

## 1. Filing a bug

### 1.1 From a running app — the meta-mode flow

While playing an app, drop into one of the bug meta modes:

| Trigger | Target | Agent |
|---|---|---|
| `/meta story bug` | the running app | `story-bug-reporter` |
| `/meta kitsoki bug` | kitsoki itself | `kitsoki-bug-reporter` |

The agent reads the per-turn `[context]` block (state, view, world,
trace file — see [`meta-mode.md`](meta-mode.md) §6), asks what was
expected vs what happened, confirms a title, and runs
`kitsoki bug create` with `--target story` or `--target kitsoki`
already filled in.

The split is in the *trigger*, not in agent reasoning — there is no
inference step, no "is this a story bug or a kitsoki bug?" dialogue.
If you file under the wrong target by mistake, the file is fine — set
`target:` in the frontmatter and `mv` the file across; the layout is
symmetric on both sides.

### 1.2 From the shell — the CLI surface

`kitsoki bug` has three subcommands. All of them resolve a
`<target-root>` per the rules below.

```
kitsoki bug create --target story|kitsoki \
  --title "one-line summary" \
  --body  "expected vs actual vs why it matters" \
  [--sink local-project|local-artifact|github] \
  [--repro "step 1" --repro "step 2" ...] \
  [--state-path main.foyer]      # story-target only
  [--app-id    cloak]            # story-target only
  [--component tui]              # kitsoki-target only
  [--severity  low|med|high]     # free-form
  [--trace-ref traces/2026-…jsonl] \
  [--target-dir <path>]          # escape hatch (overrides resolution)

kitsoki bug list --target story|kitsoki [--sink local-project|local-artifact] [--target-dir <path>]
kitsoki bug show <id>           --target story|kitsoki [--sink local-project|local-artifact] [--target-dir <path>]
```

`--target` is **required** on every subcommand — the CLI never guesses
based on cwd. The agent prompts know their own target and pass it
explicitly, so there is no ambiguity at the call site.

Target-root resolution:

- `--target story`: root is `--target-dir` if set, else `$PWD`.
  Bugs land at `<root>/issues/bugs/`.
- `--target kitsoki`: root is `--target-dir` if set, else
  `$KITSOKI_REPO`. Errors when neither is set.
- `--sink local-project` (default): use the target root above directly.
- `--sink local-artifact`: write/read the same ticket format under
  `<root>/.artifacts/issues/bugs/`. This is the preferred Kitsoki developer
  loop for high-churn local findings and evidence that should not become
  committed project tickets or GitHub issues.
- `--sink github`: create a GitHub issue; `--github <owner/repo>` is required.
  The local `list` / `show` commands inspect only `local-project` and
  `local-artifact`.

`--component` is a free-form string. The bug-reporter prompts suggest
the canonical kitsoki package names (`tui`, `host`, `transport`,
`metamode`, `app`, `agents`, `chats`) as starting points but accept
anything.

Story-only flags passed to a kitsoki bug (and vice versa) produce a
one-line stderr warning but don't fail the filing — the unrelated
flag is silently dropped from the frontmatter.

`kitsoki bug list` prints one row per bug under the selected local sink:
`<target-root>/issues/bugs/` for `local-project`, or
`<target-root>/.artifacts/issues/bugs/` for `local-artifact`. Columns are
`id`, `severity`, `status`, `title`. Sort is newest first (filenames lead
with the UTC timestamp, so lex order is chrono order). An empty or missing
directory prints nothing; not an error.

`kitsoki bug show <id>` writes the bug's markdown verbatim to stdout.
The `<id>` is the filename without `.md`. Missing file exits 1.

---

## 2. On-disk layout

```
<target-root>/
  issues/
    bugs/
      2026-05-14T103205Z-tui-hangs-on-esc.md
      2026-05-14T112011Z-foyer-banner-truncated.md
  .artifacts/
    issues/
      bugs/
        2026-07-08T091500Z-local-dogfood-finding.md
```

The extra `issues/` prefix reserves room for sibling categories
(`issues/features/`, `issues/incidents/`) without re-shuffling later.
Only `issues/bugs/` ships today; the parent dir is a stable namespace.

**Artifacts folder.** A ticket that carries binary evidence keeps a
sibling `bugs/<id>.artifacts/` directory (e.g. `screenshot.png`,
`har.json`) — *not* a folder-form ticket — so the `issues/bugs/*.md`
ticket reader is undisturbed. The body links it from a `## Artifacts`
section. Bugs filed from the web UI's Meta-menu *Report bug* surface
populate this automatically: the body carries the operator's description
plus `## Error state` and `## Console (recent)` sections, and the artifacts
folder additionally holds `rrweb.json` (a masked session replay) and
`console.json` alongside `screenshot.png` and `har.json` (see
[`../tui/web-ui.md`](../tui/web-ui.md)). The convention and the glob-safety
verification are documented in
[`issues/README.md`](../../issues/README.md) (the authoritative source)
and the web surface in [`../tui/web-ui.md`](../tui/web-ui.md).

The filename is `<UTC-timestamp>-<slug>.md`. Slug is the title
lowercased, ASCII-only, hyphenated, truncated to 60 characters. Two
bugs filed in the same second with the same title produce the same
filename, and the second `WriteFile` silently overwrites the first —
intentional, so an agent retry after a transient error is idempotent.

---

## 3. File format

Each bug is YAML frontmatter followed by markdown body.

### 3.1 Anatomy

```markdown
---
# --- identity ------------------------------------------------
id: "2026-05-14T103205Z-tui-hangs-on-esc"      # filename without .md
title: "Esc in foyer hangs the TUI"
target: "kitsoki"                              # story | kitsoki
filed_at: 2026-05-14T10:32:05Z                 # RFC 3339, UTC
filed_by: "brad"                               # $USER at write time

# --- target context ------------------------------------------
app_id: "cloak"                                # story-target only
state_path: "main.foyer"                       # story-target only
component: "tui"                               # kitsoki-target only
kitsoki_rev: "7331630"                         # kitsoki-target only, short SHA at filing time

# --- classification ------------------------------------------
severity: "med"                                # free-form; agent prompts use low|med|high
status: "open"                                 # open | resolved (default open)
labels: []                                     # free-form strings; [] when none

# --- evidence ------------------------------------------------
trace_ref: "traces/2026-05-14T103200Z-cloak.jsonl"   # relative path or session id
related: []                                    # other bug ids
---

# Esc in foyer hangs the TUI

**Expected.** Esc returns me to the meta-mode menu.

**Actual.** TUI freezes; only Ctrl-C exits.

**Why it matters.** Esc is the documented escape hatch; if it
hangs, users lose any unsaved chat input.

## Steps to reproduce

1. `kitsoki run stories/cloak/app.yaml`
2. From `main.foyer`, press `/meta kitsoki bug` to enter this mode
3. Press Esc
4. Observe: status line freezes, no further keystrokes register

## Logs / stack

\`\`\`
goroutine 1 [select]:
github.com/.../tui.(*Model).Update(...)
        internal/tui/tui.go:432 +0x1c4
…
\`\`\`
```

### 3.2 Field rules

- **Required everywhere.** `id`, `title`, `target`, `filed_at`,
  `status`. Everything else is optional. The CLI always emits the
  required set; the optional fields are emitted only when populated.
  `labels` and `related` are always emitted as YAML lists so a hand-editor
  doesn't have to re-add the key; `labels` is `[]` when the filing path did not
  provide labels.
- **Story-target required.** `app_id`. `state_path` is strongly
  encouraged. (The CLI does not hard-fail on omission; the agent
  prompts ensure both are passed.)
- **Kitsoki-target required.** `kitsoki_rev`. `component` is strongly
  encouraged. `kitsoki_rev` is populated by the CLI from
  `git -C <target-root> rev-parse --short HEAD`; if that fails (not a
  git repo, git missing) the field is left empty and a one-line
  stderr warning is printed — the bug still files successfully.
- **`status`** is a two-value flag (`open`, `resolved`). No
  in-progress / triage states — those belong in a remote tracker
  once `kitsoki bug sync` ships. A user closes a local bug by
  editing the file.
- **Unknown frontmatter keys are preserved.** The format is YAML; a
  user who adds `team: platform` keeps it across any future read
  path. Forward-compat for free.

### 3.3 What is *not* in the frontmatter

- **Long-form repro steps.** They live in the markdown body under
  `## Steps to reproduce`. YAML lists do not handle multi-line steps
  with code fences gracefully.
- **Comments / discussion in frontmatter.** Discussion is not stored in YAML.
  When a local bug file is used as a transport thread, comments are appended as
  markdown `## Comment <timestamp> by <author>` blocks after the report body.
- **Attachments.** Drop next to the markdown (e.g.
  `2026-05-14T103205Z-tui-hangs-on-esc.png`) and reference from the
  body.

---

## 4. Where the implementation lives

| Concept | Code |
|---|---|
| CLI subcommands (`create`, `list`, `show`) | [`cmd/kitsoki/bug.go`](../../cmd/kitsoki/bug.go) |
| `story-bug-reporter` agent (prompt + tool surface) | [`internal/agents/story_bug_reporter.md`](../../internal/agents/story_bug_reporter.md), [`internal/agents/story_bug_reporter.go`](../../internal/agents/story_bug_reporter.go) |
| `kitsoki-bug-reporter` agent | [`internal/agents/kitsoki_bug_reporter.md`](../../internal/agents/kitsoki_bug_reporter.md), [`internal/agents/kitsoki_bug_reporter.go`](../../internal/agents/kitsoki_bug_reporter.go) |
| Builtin `story.bug` and `kitsoki.bug` meta modes | [`internal/app/builtin_meta_modes.go`](../../internal/app/builtin_meta_modes.go) |

---

## 5. Future work — remote sync

GitHub Issues integration now lives behind the `host.gh.ticket` host and the
`kitsoki issues migrate` tooling; see
[`hosts.md` §host.gh.ticket](../architecture/hosts.md#hostghticket--github-issues-backed-tracker).
For trackers that are not wired yet, move a bug manually by copying the body
verbatim into a new issue and pasting the file path back in as a comment.
