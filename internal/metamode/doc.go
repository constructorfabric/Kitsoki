// Package metamode is the orchestrator's runtime coordinator for a
// single meta-mode chat: it sits between [kitsoki/internal/app] (which
// loads the agents: and meta_modes: declarations into an [app.AppDef])
// and [kitsoki/internal/chats] + [kitsoki/internal/host] (which persist
// transcripts and execute the LLM turn). The TUI drives it; the
// orchestrator FSM is never touched.
//
// A meta-mode chat is a side conversation with a named agent, opened as
// an overlay while a story is running. The user enters a mode ("edit
// this room", "explain this state"), exchanges turns with an agent that
// can read and edit the story tree, and exits — all without advancing
// or rewinding the underlying orchestrator. The user-facing surface
// (the agents:/meta_modes: YAML blocks, the slash commands, the
// per-turn agent contract) is documented in docs/stories/meta-mode.md;
// this package is the engine behind it.
//
// # Design
//
// Four decisions shape the package and are load-bearing for every
// method here:
//
//  1. Overlay, not transition. The orchestrator FSM is paused, never
//     teleported. [Controller.Enter] captures a [Snapshot] (state +
//     world + entered-at) and [Controller.Exit] hands it back for a
//     defensive drift check only — nothing is written back through the
//     orchestrator. The TUI "restores" by re-rendering at the saved
//     state.
//
//  2. One chat per (mode, scope). The backing chat row is keyed Room =
//     "meta:<modeName>", ScopeKey = the entry state path. Re-entering
//     the same mode from the same state resumes the same conversation,
//     so a chat is a durable thread rather than a fresh session each
//     time.
//
//  3. No orchestrator import. The controller talks to the chat store
//     through the [ChatStore] seam and to the LLM through the
//     [AgentCaller] seam — both interfaces, both injected. Tests pass
//     fakes; production passes the adapters built by
//     [NewChatStoreAdapter] and [NewAgentCallerAdapter].
//
//  4. Edits land in the tree, then commit. The agent edits files in the
//     story tree directly via its Read/Write/Edit tools.
//     [Controller.Send] diffs a pre/post snapshot of the tree to detect
//     what changed, then commits exactly those files (see
//     internal/host/meta_commit.go). When an edit lands,
//     SendResult.ReloadRequested tells the TUI to reload the
//     orchestrator before the next turn.
//
// # Algorithm
//
// One turn ([Controller.Send]) runs deterministically around the single
// non-deterministic step (the LLM call):
//
//  1. Acquire the per-chat singleton lock via [ChatStore.WithLock] so
//     the turn cannot race a queued drive dispatch or an interactive
//     `kitsoki chat attach` against the same row. Lock contention
//     returns [ErrChatBusy].
//  2. Append the user message FIRST, so a later agent failure still
//     leaves the question visible in the transcript.
//  3. Snapshot the story tree (mtime/size of every file under the app
//     dir, plus any imported sibling story dirs).
//  4. Render the [TurnContext] preamble (state, view, world, trace
//     path) and prepend it to the user text, then dispatch through
//     [AgentCaller.Ask].
//  5. Persist the (possibly new) claude session id BEFORE appending the
//     assistant reply, so a session-id write failure can't strand an
//     answered turn with no resume path.
//  6. Append the assistant reply, re-snapshot the tree, and commit the
//     changed files (new commit on the first apply per chat, amend on
//     subsequent applies — bound by a Kitsoki-Meta-Session trailer so a
//     process restart still finds the right commit).
//
// # Invariants
//
//   - The orchestrator is read-only here. No method in this package
//     calls Teleport or advances the FSM (Design decision 1).
//   - A (mode, scope) pair maps to exactly one active chat row
//     (decision 2); discovery surfaces (ListChats, ResolveChatIDPrefix)
//     and the /meta new path uphold this by archiving before opening.
//   - Transcript ordering is user-then-assistant, with the session-id
//     write fenced between (Algorithm steps 2/5/6). The same ordering
//     is used by host.runAgentAskWithMCPWithChat so transcripts stay
//     consistent across the orchestrator-driven and meta-driven paths.
//   - Meta-mode turns share the same chat lock as the rest of the chats
//     subsystem — they are not a separate arbitration regime.
//
// # Worked example
//
// Entering an "edit" mode from state "main.foyer", sending one turn,
// and exiting:
//
//	enter:  modeName="edit", snap={State:"main.foyer", World:{gold:50}}
//	resolve chat: Room="meta:edit", ScopeKey="main.foyer"  (created)
//	session: { Mode:edit, Agent:story-author, Chat:<row 01HZ…> }
//
//	send:   userText="rename the foyer to atrium"
//	preamble prepended:  state: main.foyer / world: gold: 50 / view: …
//	agent.Ask -> reply + edits app.yaml
//	tree diff: ["app.yaml"]  -> commit "meta: edit app.yaml"
//	result: { Assistant:"Renamed…", ChangedFiles:["app.yaml"],
//	          ReloadRequested:true, CommitSHA:"a1b2c3d", ChatID:"01HZ…" }
//
//	exit:   persist:true mode -> chat kept; Snapshot returned for the
//	        TUI's drift check.
//
// A runnable form of the typed seams (Enter / Send through a fake
// agent and chat store) lives in [ExampleController_Send].
//
// # Lifecycle
//
// [Controller.Enter] resolves the mode and agent and opens (or resumes)
// the chat. [Controller.Send] runs turns. [Controller.Exit] finalizes:
// persistent modes keep the chat; ephemeral modes (Persist == false)
// archive it so it leaves the default listing while staying resumable
// by full ID. [Controller.Done] and [Controller.NewChat] are the
// user-signalled "finish this chat" and "start fresh in the same scope"
// paths; both go through archive.
//
// The two seams are satisfied by the adapters in adapter.go.
// [ChatStore] wraps a *chats.Store; the adapter is a strictly smaller
// translation than internal/chathost's (which targets the larger
// host.ChatStore surface). [AgentCaller] wraps
// host.AgentAskWithMCPHandler, which imposes three constraints the
// adapter bridges: the handler reads its prompt from a file (so the
// adapter materialises the system-prompt-prefixed user message to a
// tempfile), has no native SystemPrompt arg (so the system prompt is
// prefixed into the prompt body with a separator), and does not gate by
// tool name (so the agent's tool allowlist is threaded through as a
// visible hint, not a runtime gate).
//
// The dependency packages are: internal/agents (name-keyed [agents.Agent]
// registry bundling system prompt, model, tool surface, and default
// cwd) and internal/app (loads meta_modes: into [app.AppDef.MetaModes];
// app.SessionID and app.StatePath live in internal/app/types.go,
// [world.World] in internal/world).
//
// # Non-goals
//
//   - No orchestrator transition or teleport. The package is overlay
//     only by design (Design decision 1); restoring state is the TUI's
//     re-render, not a write-back through the FSM.
//   - No claude session resume on the LLM seam yet. AskInput.ClaudeSessionID
//     is captured and plumbed (so it can be asserted and so the chat
//     row records it), but host.AgentAskWithMCPHandler's non-chat path
//     does not yet honour it; turn-to-turn Claude memory across a meta
//     chat is a tracked follow-up, not a current guarantee.
//   - No multi-language / localized prompt support. The preamble and
//     separators are English; localization is the demo-template layer's
//     concern, not this coordinator's.
//   - No in-process proposal ledger persisted across app restarts. Edits
//     are committed to the git tree per turn; the durable record is the
//     commit history, not an in-memory draft store that has to survive
//     a restart.
//
// All four are deliberate: the coordinator's job is to be a thin, typed,
// auditable seam between the TUI and the chats/host machinery. Each
// restriction keeps the orchestrator's state authority and the git tree
// (not this package) as the sources of truth.
//
// # Reference
//
// The user-facing reference — the agents:/meta_modes: YAML blocks, the
// builtin story-author mode, the slash commands, the per-turn agent
// contract, and the agent-verb mapping — is docs/stories/meta-mode.md.
// The agent registry is [kitsoki/internal/agents]; the chat store is
// [kitsoki/internal/chats]; the LLM dispatch handler is in
// [kitsoki/internal/host].
package metamode
