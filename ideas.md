## Done

- meta mode: ask questions or improve the story itself (replaces edit mode)
- meta mode works like off path so it's a full convo, can use all normal conventions like proposal
- meta mode chats are persistent and listed like oracle sessions
- meta mode as a generic concept (off-path, self-fix/improvement/extend) — each meta mode has different agent and prompts
- self mode: ask questions or improve kitsoki itself
- use specific claude agents for each room/intent where oracle/claude is invoked
- extensible stories — reusable dev story w/ company and project-specific aspects (rooms, intents, etc... as reusable building blocks — extended and composed)
- add bug report mode
- --continue to resume existing session
- input history via up/down arrow
- add reload so external app changes can be picked up without quit/restart (keep world as-is so state is preserved)
- provide context/guidance/prompt to off_path based on current room + provide history/context/etc...
- dedupe and integrate docs (cmd folder and main project root)
- check that we're really doing the mcp validation method — i think we're maybe not based on some bugs

## Partial / in progress

- ticket/pr/etc... providers that support bitbucket jira github or file mode for testing/dev match our existing bugfix artifact write pattern — bitbucket + jira + tui done; github TBD
- voice/setting theming and localization — different languages and oregon trail could be in space (typed-elements + pongo2 foundation in place across demo apps)
- file story bug or kitsoki bug with a similar interface/pattern, if kitsoki is local write to file (we are in dev mode and stories + kitsoki source are local) — story bug done via `/meta bug`; kitsoki target in proposal (`docs/proposals/bug-format-proposal.md`)
- add world display to TUI — apps can specify what state is shown in the panel, make it like the actions panel — `relevant_world` pins to location indicator today; not yet a full panel
- background jobs on VMs, dispatch and track, survive intermittent connectivity (dev laptop to VM w/ VPN and closed lid issues) — local background jobs done; VM/remote survival TODO
- recording captures git commits and LLM interactions so it's possible to do a deterministic replay, graceful call to LLM if it's a new/changed call that needs a real response to be recorded — LLM interactions captured; git commits TBD
- json-rpc, mcp, rest support w/ auth — mcp done; rest + json-rpc TBD
- jsonschema for stories/rooms/etc... and good validation mode in CLI, semantic validation too, validate pongo2 templates — app-schema doc + basic validate exist; jsonschema + semantic + template validation TBD

## Ideas

- generate test from trace
- generate precursor recording/state so we can continue right at the point where a new feature is to be demoed or a bug is reproduced
- trace includes atomic state updates in some json-diff format so that there can be checkpoints + event stream for balance between size and speed of replay (reprocessing events) — event sourcing model for consistency
- pure in-memory apps that are mutable in-flight, fully dynamic apps, export to yaml
- in proposal review mode, make the input and proposal different colors from the rest of the text
- when user presses enter, immediately add their input into the chat window and show thinking there, block input until resolved (can keep some spinner in the input area too)
- visually distinguish between user commands that were interpreted deterministically vs those that use the LLM, when the LLM is used show the actual filled intent that was selected w/ confidence level
- cache of natural language to intents to avoid calling claude again
- intent synonyms and caching
- multi-intent — when actions/intents are non-navigational they can be stacked within a single input — on Oregon Trail it's like name the party, define the profession and start month in a single statement
- single LLM chat across rooms, manage the scope to determine which rooms are contained within the chat, provides better context and richer history without hacks like the history mcp
- live discovery of story aspects as the user navigates to different projects, projects can define their own story aspects
- expose oracle API so scripts can funnel their LLM usage via the standard interface instead of invoking claude -p individually, possibly bypassing configuration. this would mean that scripts can use a generic API, and the interface can choose codex vs claude (in the future), and handle the tracing, playback and testing with a standardized mechanism. scripts would then never use claude directly
- pass request id to downstream CLI and API calls so that the session/trace can be correlated, so for instance mcp validator can log directly against the right session (is this racy?)
- conversation/session info/context mcp for LLM to use for clarification
- better testing for proposal mode — should work like conversation (w/ persistent convo)
- remote job mode: monitor and control sessions on VMs
- open a VS Code over Remote Tunnel or to local folder for a given project/PR/etc...
- vs code integration like claude, see what the user has open and propose changes using VS code diff
- react UI
- voice/speaker transport
- live vs local mode for transports — sometimes we just want to work locally and don't need jira/bitbucket comments
- these providers are behind mcp for use in sessions, pluggable backends with the same interface so a single prompt works across different implementations
- official test suite (makefile) and build, etc... lint/static analysis, etc... prepare for CI
- LLM-driven local review checklist, docs, testing, etc... add it to devstory to make it real
- remove world from top status bar it crowds everything
- actions panel is too narrow
- chat input doesn't wrap it just disappears off the end of the screen
- can't use numbers as start of chat text
- oversight/silent-background LLM sessions watching the trace internally for certain patterns or insights to file bugs, improvement or knowledge, watch the transcript live, add configurable prompts to watch out for bad patterns and jump in with guidance (when the LLM ignores CLAUDE.md for example or some other annoying pattern).  it's possible to list/attach the sessions but not normally (behind some extra arg), and the only normal output is async artifacts like bugs, knowledge, etc... and then the user gets an inbox notification that a bg agent has created some artifact and they can review it.  self-improvement, bugs, new synonyms, etc... can all be done like this.  include non-LLM (script) actions that can also produce artifacts and notify the user based on some schedule/precursor or LLM pattern detection trigger (tell the LLM to find some pattern and when it does call a script that does some thing)
- make sure full deterministic replay so we can do bugfix test runs w/o actually using LLM - capture git diffs, etc... for replayable scenarios (like we did PLTFRM-89912 ad infinitum)
- llm conversation vs decision, separate interfaces, conversation/task work wouldn't be done by humans but decisions may be (and the decision schema may drive a form for example)
