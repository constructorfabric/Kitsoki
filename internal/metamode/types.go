// Package metamode implements the runtime coordinator for a single
// meta-mode chat: entering it (snapshot + chat resolve), conversing
// inside it (Send), and exiting (cleanup of any session-scoped
// authoring proposals).
//
// This is workstream WS-A3 of the meta-mode proposal (see
// docs/proposals/meta-mode-proposal.md §2–§3). Two preceding pieces are
// already merged on feat/meta-mode:
//
//   - internal/agents (WS-A1) supplies a name-keyed Agent registry whose
//     Agent struct bundles the system prompt, model, declared tool
//     surface, and an optional default cwd.
//   - internal/app (WS-A2) loads the meta_modes: YAML block into
//     app.AppDef.MetaModes (a name → *app.MetaModeDef map). Helpers
//     PersistOrDefault / ExitIntentOrDefault live there.
//
// Locked design decisions (per the WS-A3 brief):
//
//  1. The meta mode is a TUI-side overlay; the orchestrator FSM is
//     paused, never transitioned. Enter captures a Snapshot (state +
//     world + entered-at timestamp); Exit returns it for defensive
//     comparison only. No Teleport call in Phase A — the snapshot's
//     job is "did the orchestrator drift while we were away?", not
//     "restore by writing back".
//
//  2. Chat key shape: Room = "meta:<modeName>", ScopeKey = the entry
//     state path. Re-entering the same mode from the same state
//     resumes the same chat (proposal §2.1: single active session per
//     mode per story).
//
//  3. The controller does not call the orchestrator. It uses the chats
//     store (via ChatStore adapter) for transcript persistence and the
//     oracle (via OracleCaller adapter) for the actual LLM dispatch.
//
//  4. Pending authoring proposals are session-scoped, tracked on a
//     ProposalLedger that lives on the Session. WS-A4 will wire the
//     authoring.{propose,apply,discard} tool handlers into this
//     ledger; WS-A3 leaves the shape ready but does not implement
//     those handlers.
//
//  5. SendResult.ReloadRequested is the contract WS-A4 and WS-A5 will
//     use to ask the TUI to reload the orchestrator after a
//     successful authoring.apply. WS-A3 always sets it false.
//
// Resolved imports (the brief listed sketch types; here are the real
// names used in this codebase):
//
//   - app.SessionID is defined in internal/app/types.go.
//   - app.StatePath is also defined there (slash-separated state path,
//     e.g. "bar/dark").
//   - world.World lives in internal/world/world.go (a snapshot struct
//     with a Vars map[string]any).
package metamode

import (
	"time"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/world"
)

// Snapshot is the orchestrator state captured at the moment of Enter.
// On Exit the controller returns the saved Snapshot so the TUI can
// confirm the live orchestrator hasn't drifted; in Phase A nothing is
// written back through the orchestrator. The TUI restores by simply
// re-rendering at the saved state.
type Snapshot struct {
	SessionID app.SessionID
	State     app.StatePath
	World     world.World
	EnteredAt time.Time
}

// Session is the live coordinator state for one in-progress meta-mode
// chat. Returned by Controller.Enter, threaded through Send, and
// finalized by Exit.
type Session struct {
	// Mode is the resolved meta-mode declaration (from
	// app.AppDef.MetaModes).
	Mode *app.MetaModeDef
	// Agent is the resolved agent definition (system prompt + tool
	// surface + default cwd).
	Agent agents.Agent
	// Chat is the persistent chat row backing this session. Its
	// ScopeKey matches the entry state path so re-entry from the
	// same state resumes the same conversation.
	Chat ChatHandle
	// Snapshot is the state captured at Enter — used by the TUI on
	// Exit to defensively compare against the live orchestrator.
	Snapshot Snapshot
	// Ledger tracks authoring proposals raised during this session.
	// WS-A4 will add/get/discard entries via the controller wiring.
	Ledger *ProposalLedger
}

// TurnContext is the per-turn ambient state the TUI threads into
// Controller.Send. It feeds the [context] block prepended to the user
// message so the agent can describe location, reason about state, and
// emit propose tokens with the right app_file path.
//
// All fields are optional. Empty fields are omitted from the rendered
// preamble entirely (no "state: \"\"" placeholder lines). When AppFile
// is non-empty, the propose-token parser auto-fills it on calls that
// omit app_file in their JSON — defense-in-depth against the agent
// forgetting to set it.
type TurnContext struct {
	// StatePath is the current FSM state path (e.g. "main.foyer"). The
	// TUI passes m.currentState; in tests this can be empty.
	StatePath string
	// AppFile is the absolute path to the app.yaml the propose tools
	// should target. The TUI passes m.appPath; tests pass "" or a
	// fixture path.
	AppFile string
	// RenderedView is the markdown view the user is currently
	// looking at — typically the result of
	// orchestrator.Machine().RenderState(currentState, world). Rendered
	// inside the preamble as a YAML literal block so newlines survive.
	RenderedView string
	// World is the resolved world snapshot. Rendered as YAML-ish
	// key:value lines, with each value truncated to 200 chars.
	World map[string]any
	// TracePath is the absolute path to a JSONL file the TUI just
	// wrote containing the recent trace events for this session.
	// The agent can Read it for context on what happened in the
	// session so far (state transitions, host calls, world
	// mutations). Empty when no trace ring is available (non-TUI
	// callers like `kitsoki turn` and tests).
	TracePath string

	// ImportedManifestPaths is the absolute-path list of every
	// imported child manifest the loader visited during the current
	// AppDef's recursive load (story-imports proposal §16.4). The
	// metamode controller folds the parent directories of each into
	// its post-call snapshot tree so an edit in a sibling story (e.g.
	// `stories/robbery/` while running `stories/oregon-trail/`)
	// triggers an auto-reload. Empty when the app declares no imports
	// or when the caller hasn't populated it; in either case the
	// behaviour falls back to the single-root walk of AppFile's dir.
	ImportedManifestPaths []string
}

// SendResult is the per-turn outcome surfaced to the TUI.
//
// ReloadRequested is the WS-A4 ↔ WS-A5 handshake: when an
// authoring.apply tool call succeeds inside a turn (Phase B), the
// authoring side flips this bit so the TUI knows to reload the
// orchestrator's AppDef from disk and refresh its rendering before
// the next turn. WS-A3 always emits false.
type SendResult struct {
	Assistant       string
	ReloadRequested bool
	// ChangedFiles lists the paths (relative to the story directory)
	// whose mtime/size changed during this turn — typically because
	// the agent edited them via Read/Write/Edit. Includes app.yaml,
	// included YAML fragments, prompts, scripts, anything the
	// directory walk picked up. Empty when nothing changed.
	ChangedFiles []string
	// CommitSHA / CommitAmended / CommitError carry the outcome of the
	// deterministic post-turn git-commit step. When any files changed,
	// the controller stages exactly those files and either creates a
	// new commit (first apply per chat) or amends HEAD (subsequent
	// apply in the same chat — keyed on a Kitsoki-Meta-Session trailer
	// so process restarts don't lose the binding). CommitSHA is empty
	// when no commit happened (no changes, no git repo, or git
	// failed — CommitError disambiguates).
	CommitSHA     string
	CommitAmended bool
	CommitError   string
	// ChatID is the meta-chat row's full ULID. Surfaced on every
	// successful turn so the TUI can render a "kitsoki chat attach
	// <id>" hint — meta-mode chats are regular chat rows under the
	// hood, so the user can hand off to an interactive
	// claude --resume session against the same conversation history.
	ChatID string
	Err    error
}

// ChatHandle is the tiny subset of the chat row the controller needs.
// Implemented by an adapter over internal/chats so the controller
// stays free of an internal/chats import.
//
// AppendMessage's role is one of "user" | "assistant" | "system" |
// "tool" to match internal/chats.Message semantics.
//
// Title / UpdatedAt / FirstUserMessage support the Phase A.5 listing
// surface (/meta list). They sit on the handle so the controller can
// produce ChatListing rows without re-importing internal/chats.
type ChatHandle interface {
	ID() string
	AppID() string
	Room() string
	ScopeKey() string
	Title() string
	UpdatedAt() time.Time
	ClaudeSessionID() string
	SetClaudeSessionID(string) error
	AppendMessage(role, text string) error
	// FirstUserMessage returns the content of the first user-role
	// message in the transcript, or "" if no user turn has been
	// recorded yet. Implementations may read this lazily.
	FirstUserMessage() (string, error)
}
