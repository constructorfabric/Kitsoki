The dev-story is an application that harnesses the day-to-day work of a software engineer in the AI era.

It is styled after a text-dungeon, where different activities are in different physical areas, and you can travel deeper into an area or take actions from a given spot.

# Main Room

The user enters the main room as their primary landing place.  It provides the top-level navigation for the possible activities.  Devs will come back here often throughout the day as they try to find something to do.

## Intents

- Start a new task: go to the jira search room
- Continue an existing task: go to the workspace-manager room
- Consult the oracle: go to the oracle room for general-level context discussion w/ Claude
- Review a teammate's PR: go to the code review room
- Check my inbox: go to the inbox room for unread Jira/GitHub/Slack notifications
- Prep for standup: go to the standup room to summarize yesterday's work and today's plan
- Triage a production issue: go to the incident room

# Jira Search Room

The user enters the jira search room when they're trying to find one or more tickets in jira.  The user may have various intents upon entering the room:

## Intents

- Find a ticket to start working on - create a new workspace and go to the workspace room
- Find tickets with information relevant to an existing task - produce a summary report and return to their previous room
- Find tickets to investigate a pattern or problem - produce a summary report and 

A user may iteratively refine the jira search (updating the JQL query).

# Workspace Manager Room

The user enters the workspace manager room when they're looking for an existing, in progress workspace to enter.  They may also enter the workspace manager room when they want to create a new workspace for a purpose or a known task.

## Intents

- Find an existing workspace to enter - enter that workspace room
- Create a new workspace for an idea or an existing item - enter that workspace room

# Workspace Room

The user enters the workspace room when they're working on a particular workspace (project).  It can be a flat workspace with a single repo, or a multi-repo workspace in an orchestration repo.  The overall functionality is similar.

## Intents

- Open a new VS Code instance in the workspace root
- Go to the terminal room
- Check the PR status (multi-repo workspaces have a shared PR feature)
- Find a ticket (related to the project) - go to the Jira search room with the intent to find something in the specific project
- Fix a bug - enter the bug fix room with the intent to work on a specific bug
- Implement a task - enter the implementation room with the intent to work on a specific issue
- Debug an active issue - go to the debug room with the workspace context loaded
- Write or update tests - go to the test room
- Update documentation - go to the docs room
- Refactor code - go to the refactor room to propose and apply changes without altering behavior
- Sync with remote - pull latest, update submodules, rebase in-progress branches
- View logs or metrics - go to the observability room scoped to the workspace's services
- Plan an approach - go to the planning room to break down an issue before implementation
- Deploy changes - go to the deploy room

# Terminal Room

The user enters the terminal room to have a free-form zsh session where the CLI commands are generated and formatted by the LLM, but then run deterministically with normal output display.  The goal is to replace a structured CLI command with a human input, and to possibly review and improve that CLI command iteratively before running it, or after running it.

## Intents

- Run a command
- Re-Run a command with or without feedback

# PR Room

The user enters the PR room when they're reviewing or improving an existing PR.  It is entered when a user needs to review feedback, check status, or fix CI issues or resolve or reply to comments.

## Intents

- Check status - deterministically fetch the PR(s) status
- Fix CI issues - use LLM to resolve CI and bot comments
- Resolve comments - use LLM to resolve human comments and respond
- Rebase or merge in main - bring the branch up to date and resolve conflicts
- Update the PR description - regenerate summary/test-plan from the commit range
- Request review - pick reviewers based on CODEOWNERS and recent file history
- Mark ready / draft - toggle PR status
- Merge the PR - run the merge (squash/rebase per repo policy) after checks pass

# Oracle Room

The user enters the oracle room for open-ended conversation with Claude.  This is the catch-all for questions that don't fit a more specific room: architectural brainstorming, "how does X work", rubber-ducking, or exploring an unfamiliar area of the codebase.  No workspace is required.

## Intents

- Ask a general question - free-form Q&A with access to read-only code and doc search
- Explore an unfamiliar area - guided walkthrough of a module, service, or flow
- Brainstorm an approach - discuss trade-offs before committing to a plan

# Bug Fix Room

The user enters the bug fix room to work on a specific bug within a workspace.  The goal is to reproduce, localize, fix, and verify.

## Intents

- Reproduce the bug - write or run a failing test or repro script
- Localize the root cause - trace through logs, stack traces, and suspect code
- Apply a fix - make the minimal change that resolves the root cause
- Verify the fix - re-run the repro and related tests
- Open a PR - hand off to the PR room with the fix branch

# Implementation Room

The user enters the implementation room to build a new feature or complete a task defined by a Jira issue or internal plan.

## Intents

- Review the task - load the Jira issue, linked designs, and acceptance criteria
- Draft a plan - go to the planning room with the task context pre-loaded
- Write the code - iterative edit/run/test loop with the LLM
- Write tests - go to the test room with the feature context
- Open a PR - hand off to the PR room with the feature branch

# Planning Room

The user enters the planning room to break down an issue, epic, or idea into an actionable approach before writing code.  Output is a plan that can drive implementation.

## Intents

- Break down an epic - produce a list of child issues with scope and dependencies
- Design an approach - discuss architecture, interfaces, and trade-offs for a task
- Estimate effort - get a rough size based on similar past work
- Save the plan - attach to the Jira issue or the workspace

# Debug Room

The user enters the debug room to investigate a specific failure: a failing test, a production error, a stack trace, or unexpected behavior.  Distinct from the bug fix room in that no fix is committed here — only investigation.

## Intents

- Analyze a stack trace or error - paste and get a guided explanation with code refs
- Search logs - query the observability backend for related log lines
- Bisect a regression - find the commit that introduced a failure
- Inspect state - run targeted queries or scripts to check live or local state
- Hand off to bug fix room - carry the findings into an actual fix

# Test Room

The user enters the test room to write, run, or maintain tests for a workspace.

## Intents

- Run the suite - deterministically run unit/integration tests with streamed output
- Run a focused test - re-run a single failing test with verbose output
- Write new tests - generate tests for uncovered code paths
- Fix a flaky test - diagnose and stabilize a known-flaky test
- Update snapshots or fixtures - regenerate after intentional behavior changes

# Code Review Room

The user enters the code review room to review PRs authored by teammates.  Distinct from the PR room (which is for the user's own PRs).

## Intents

- List PRs awaiting my review - pull from GitHub across the user's repos
- Review a specific PR - summary, diff walkthrough, and suggested comments
- Leave review comments - post inline comments or an overall review
- Approve or request changes - submit the review verdict

# Standup Room

The user enters the standup room to prepare for daily standup or async status updates.

## Intents

- Summarize yesterday - aggregate commits, PRs, and Jira transitions from the last day
- Plan today - surface in-progress workspaces and assigned Jira issues
- List blockers - highlight PRs stuck in review, failing CI, or waiting on others
- Post to Slack - send the generated update to the team channel

# Inbox Room

The user enters the inbox room to triage their unified attention queue.  The inbox is the in-app mechanism for surfacing anything that needs the user's attention regardless of which room produced it.  It unifies three streams:

- **Completed background jobs** - a deploy finished, a test suite passed, an LLM draft is ready for review
- **Clarification requests** - a background job stalled because it needs input from the user before it can continue
- **External notifications** - GitHub review requests, Jira assignments, Slack mentions

Every inbox item carries a teleport target: opening the item transitions the user back into the originating room with the relevant proposal, job, or external context rehydrated so they can continue where the work left off.

The inbox is visible from everywhere - a status-line badge in every room shows unread count and whether anything is `action_required`.  High-severity items (clarification requests, failed jobs) may briefly surface an interrupt prompt in the current room rather than waiting to be discovered.

## Intents

- Show unread items - unified list across all three streams
- Open an item - teleport to the source room with context restored (proposal/job rehydrated)
- Answer a clarification - submit the requested input and resume the stalled job
- Dismiss or snooze - mark-read or defer an item without acting on it
- Cancel a running job - stop a background job from the inbox view
- Bulk archive - clear stale completed/read items

# Docs Room

The user enters the docs room to write or update documentation: in-repo Markdown, README files, or Confluence pages.

## Intents

- Update in-repo docs - edit Markdown/README alongside a code change
- Update a Confluence page - edit a page the workspace is linked to
- Generate docs from code - produce an initial draft from source (API, module, flow)
- Cross-link - add references between Jira, Confluence, and the repo

# Deploy Room

The user enters the deploy room to ship changes to an environment, check what's deployed, or roll back.

## Intents

- Check what's deployed - compare deployed revision to main across envs
- Trigger a deploy - kick off the pipeline for a target environment
- Watch a deploy - stream pipeline and rollout status
- Roll back - revert to the previous known-good revision

# Observability Room

The user enters the observability room to check logs, metrics, traces, or dashboards for a service.

## Intents

- Tail logs - stream logs for a pod/service, optionally filtered
- Check a dashboard - open the relevant Grafana/metrics board
- Query traces - find slow or failing requests
- Check alerts - see firing alerts relevant to the workspace's services

# Incident Room

The user enters the incident room when responding to a production issue or on-call page.  Goal is fast context and coordinated response.

## Intents

- Load incident context - pull the alert, recent deploys, and related dashboards
- Identify recent changes - list deploys and merges in the last N hours
- Mitigate - roll back, toggle a feature flag, or scale a service
- Update stakeholders - post status to the incident Slack channel
- Open a postmortem - create the postmortem doc seeded with timeline

# Refactor Room

The user enters the refactor room to make structural changes to code without altering behavior.

## Intents

- Propose a refactor - describe intent and get a change plan
- Apply the refactor - execute the changes with test guards
- Verify behavior is unchanged - run full test suite and compare outputs where applicable
- Split into reviewable commits - break the refactor into atomic, easy-to-review pieces
