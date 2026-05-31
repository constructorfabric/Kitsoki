package metamode

import (
	"time"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/world"
)

// Snapshot is the orchestrator state captured at the moment of Enter.
// On Exit the controller returns the saved Snapshot so the TUI can
// confirm the live orchestrator hasn't drifted; nothing is ever written
// back through the orchestrator (overlay-only — see the package doc).
// The TUI restores by simply re-rendering at the saved state.
type Snapshot struct {
	// SessionID identifies the orchestrator session this overlay sits on.
	SessionID app.SessionID
	// State is the FSM state path at Enter — also the chat ScopeKey, so
	// re-entry from the same state resumes the same chat.
	State app.StatePath
	// World is the world snapshot at Enter, for the Exit drift check.
	World world.World
	// EnteredAt is the entry timestamp; Enter stamps it from the
	// controller clock when the caller leaves it zero.
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
	// AppDef's recursive load (the story-imports auto-watch surface). The
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
// ReloadRequested is the edit-to-reload handshake: when an edit lands
// inside a turn (the agent wrote to the story tree), the controller
// flips this bit so the TUI knows to reload the orchestrator's AppDef
// from disk and refresh its rendering before the next turn. A turn that
// changed no files emits false.
type SendResult struct {
	// Assistant is the LLM reply text (empty on error).
	Assistant string
	// ReloadRequested asks the TUI to reload the orchestrator's AppDef
	// before the next turn because an edit landed this turn.
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
	// Err carries the turn's failure, if any. The other fields are only
	// meaningful when Err is nil.
	Err error
}

// ChatHandle is the tiny subset of the chat row the controller needs.
// Implemented by an adapter over internal/chats so the controller
// stays free of an internal/chats import.
//
// AppendMessage's role is one of "user" | "assistant" | "system" |
// "tool" to match internal/chats.Message semantics.
//
// Title / UpdatedAt / FirstUserMessage support the chat-listing
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
