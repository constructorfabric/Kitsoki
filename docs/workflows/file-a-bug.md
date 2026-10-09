# File a bug workflow

Get a reproducible, evidence-backed bug report on disk (or into GitHub
Issues) with minimal friction, from wherever you noticed the problem. The
full format, filing targets (`story` vs `kitsoki`), and CLI reference are
documented once in [`../stories/bugs.md`](../stories/bugs.md) — this page
is only "how you trigger filing from each surface."

Two distinct filing mechanisms exist, and every surface below uses one or
the other:

- **Agentic meta-mode filing** — `/meta story bug` or `/meta kitsoki bug`
  drops you into a conversation with a bug-reporter agent that helps formulate
  what was expected vs. what happened and runs `kitsoki bug create` for you. See
  [`../stories/meta-mode.md`](../stories/meta-mode.md) §3.2 for the agent/mode
  details.
- **Deterministic evidence capture** — the web/VS Code **Report bug** modal
  captures an rrweb session replay, a scrubbed HAR, console/error state, and
  session trace evidence automatically, then generates an
  `Evidence-derived triage` section with likely repro steps, observed actual
  behavior, and an explicit expected-behavior fallback when the evidence cannot
  prove it. It shows you a **review-before-file** modal — nothing is written
  until you click Submit. See
  [`../tui/web-ui.md#meta-menu--report-bug`](../tui/web-ui.md#meta-menu--report-bug)
  for exactly what's captured and anonymized.

Both local sinks land the same markdown shape:
`issues/bugs/<id>.md` (+ a sibling `<id>.artifacts/` folder for evidence) —
see [`../stories/bugs.md` §2–3](../stories/bugs.md) for the full layout and
frontmatter contract. For high-churn Kitsoki developer iteration, prefer the
artifact sink: `--sink local-artifact` writes that same tree under
`.artifacts/issues/bugs/`, so findings and evidence are durable locally but are
not committed or filed to GitHub.

GitHub filing is optional and uses the same local auth setup across every
surface. The easiest local setup requires GitHub CLI (`gh`) and uses its
browser/PIN login:

```sh
kitsoki gh-agent login
source ~/.config/kitsoki/github.env
```

What you do: approve GitHub CLI auth in the browser, or enter the one-time
code GitHub CLI prints. What Kitsoki does: copy `gh auth token` into the local
env file used by `--github`, `--ticket-repo`, TUI `/bug` GitHub filing, web
Report bug, and MCP issue filing.

For repo-limited GitHub App auth instead, run:

```sh
kitsoki gh-agent setup app --name <app-name> --local-only
kitsoki gh-agent setup attach --repo owner/name
kitsoki gh-agent token
source ~/.config/kitsoki/github.env
```

If a token is missing, the filing surface returns the same setup hint instead
of silently dropping the report. For PAT fallback, set `GH_TOKEN` or
`GITHUB_TOKEN` to a fine-grained PAT and run `kitsoki gh-agent token --from-env`.

## TUI

While playing any app, drop into a bug meta mode:

```
/meta story bug       # bug in the app you're playing (story-bug-reporter agent)
/meta kitsoki bug     # bug in kitsoki itself (kitsoki-bug-reporter agent)
```

The agent reads the per-turn `[context]` block (state, view, world, trace
file), asks what was expected vs. actual, confirms a title, and files via
`kitsoki bug create --target story|kitsoki`. From the shell directly (no
running session needed):

```
kitsoki bug create --target story \
  --title "one-line summary" --body "expected vs actual vs why it matters"
kitsoki bug list  --target story
kitsoki bug show  <id> --target story
```

For local Kitsoki dogfood and iterative stabilization, keep the ticket out of
the committed project tree:

```
kitsoki bug create --target story --sink local-artifact \
  --title "one-line summary" --body "expected vs actual vs why it matters"
kitsoki bug list  --target story --sink local-artifact
kitsoki bug show  <id> --target story --sink local-artifact
```

See [`../stories/bugs.md` §1](../stories/bugs.md#1-filing-a-bug) for the
full CLI surface (`--repro`, `--severity`, `--trace-ref`, target-root
resolution rules, etc).

To file straight to **GitHub Issues** instead of a local file, pass
`--github <owner/repo>`; see
[`../architecture/hosts.md#hostghticket--github-issues-backed-tracker`](../architecture/hosts.md#hostghticket--github-issues-backed-tracker).

## Web

The Meta dropdown (bottom-right) has a **Report bug** item — click it to
open the review modal (rrweb replay + HAR + console/error state + trace
evidence, anonymized), add an optional short description, and **Submit** to file
or **Cancel** to discard. Nothing is written until Submit. On submit, the server
adds deterministic `Evidence-derived triage` to the filed report, so reporters
do not have to hand-author expected/actual/repro fields for evidence-backed
bugs. Run with
`kitsoki web --ticket-repo <owner/repo>` to file to GitHub Issues instead of
a local file. See
[`../tui/web-ui.md#meta-menu--report-bug`](../tui/web-ui.md#meta-menu--report-bug)
for the full capture/anonymize/file contract. The item is hidden in
snapshot/artifact (read-only) mode — there is no live session to capture
from there.

## VS Code

The extension embeds the same web SPA in a webview panel (see
[`../tui/vscode-extension.md`](../tui/vscode-extension.md)), so the Meta →
**Report bug** modal above is reachable identically inside the editor.
**Current caveat:** there is no dedicated VS Code proof of this path (no
extension-specific e2e spec) — it rides the web SPA's own coverage rather
than having its own.

## gh-agent

Filing a bug does not need `@kitsoki` at all — file to GitHub Issues
directly from the TUI (`kitsoki bug create --github <owner/repo>`) or the
web modal (`kitsoki web --ticket-repo <owner/repo>`). Once filed, the
gh-agent's **bug-report deck** feature auto-triggers: when the filed
issue's evidence includes an rrweb replay + HAR (the artifacts the web
modal produces), `internal/ghagent/bugdeck` deterministically (no LLM, no
paid calls) renders them into a single self-contained, in-browser-playable
slidey deck and comments a link on the issue. See
[`internal/ghagent/bugdeck/doc.go`](../../internal/ghagent/bugdeck/doc.go)
for the pipeline and
[`../architecture/github-agent.md`](../architecture/github-agent.md) for
where this sits in the broader dispatch loop.

## Standing proof status

See the `file-bug` row of
[`../testing/dev-workflow-matrix.md`](../testing/dev-workflow-matrix.md).
