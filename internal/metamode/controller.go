package metamode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kitsoki/internal/agents"
	"kitsoki/internal/app"
	"kitsoki/internal/authoring"
	"kitsoki/internal/host"
	"kitsoki/internal/journal"
)

// osStat is a package-local indirection so tests can swap the fs
// without taking a build-tag dependency.
var osStat = os.Stat

// Controller orchestrates Enter / Send / Exit for meta-mode chats. It
// holds the pluggable seams (chat store, agent registry, app
// definition, oracle, clock) and contains no transport-specific code
// itself. Tests inject fakes; production wiring uses the adapters in
// adapter.go.
type Controller struct {
	// Chats resolves or creates the chat row backing a meta-mode
	// session.
	Chats ChatStore
	// Agents resolves agent definitions by name.
	Agents agents.Registry
	// AppDef supplies the loaded meta_modes: declarations.
	AppDef *app.AppDef
	// Oracle issues a single LLM turn. Implementation owns the
	// claude shellout (or, in tests, a fake).
	Oracle OracleCaller
	// Clock is the time source for Snapshot.EnteredAt (and any future
	// timestamps). Defaults to time.Now when zero.
	Clock func() time.Time
	// JournalWriter, when non-nil, is threaded into each ProposalLedger
	// created by Enter/New/Resume so ledger lifecycle events (staged /
	// discarded / applied) emit typed journal entries (continue-mode §4.7 v3).
	// Nil disables ledger journal writes (back-compat default).
	JournalWriter journal.Writer
}

// ChatStore is the controller-facing chat store seam. ResolveMeta
// covers Enter; GetMeta / ListMeta / ArchiveMeta cover the Phase A.5
// discovery surface (/meta list, /meta resume, /meta new). WithLock
// is the singleton-lock primitive shared with the rest of the chats
// subsystem — see Controller.Send for why meta-mode turns now
// acquire it.
type ChatStore interface {
	// ResolveMeta returns the chat row for (appID, room, scopeKey),
	// creating it with the given title if it doesn't exist. Room
	// here is already the "meta:<modeName>" string.
	ResolveMeta(ctx context.Context, appID, room, scopeKey, title string) (ChatHandle, error)
	// GetMeta fetches the chat row by full ID. Errors if not found
	// or not a meta chat (Room without the "meta:" prefix).
	GetMeta(ctx context.Context, chatID string) (ChatHandle, error)
	// ListMeta returns every active meta chat for the app, sorted by
	// the implementation (the controller re-sorts before surfacing).
	// "Meta chat" means Room HAS PREFIX "meta:". Archived rows are
	// excluded.
	ListMeta(ctx context.Context, appID string) ([]ChatHandle, error)
	// ArchiveMeta soft-deletes the chat by ID (status → archived).
	// /meta new uses this before opening a fresh row in the same
	// scope.
	ArchiveMeta(ctx context.Context, chatID string) error
	// WithLock acquires the per-chat singleton lock, runs fn, and
	// releases. Same primitive used by chats.Store.WithLock and the
	// drive dispatcher — meta-mode joins the same arbitration regime
	// so a meta turn can't race a `kitsoki chat continue`, a queued
	// drive dispatch, or a `kitsoki chat attach` session against the
	// same chat row. On lock contention, fn is not called and the
	// returned error wraps ErrChatBusy.
	WithLock(ctx context.Context, chatID string, fn func(context.Context) error) error
}

// ErrChatBusy is returned (wrapped) by Controller.Send when the chat
// lock is held by another driver. Callers (the TUI metaSendCmd path)
// should use errors.Is to detect it and render a busy-chat message
// rather than the generic "oracle ask: …" wrapper.
var ErrChatBusy = errors.New("metamode: chat busy")

// OracleCaller is the controller-facing LLM seam. The Ask method
// represents one turn against an agent: system prompt + user message
// in, reply + new claude session id out.
//
// The adapter in adapter.go implements this against
// host.OracleAskWithMCPHandler. See adapter.go's package comment for
// the constraints the real handler imposes (no native SystemPrompt
// arg, no native tool-allowlist arg on the non-chat path) — the
// adapter does the translation so the controller stays typed.
type OracleCaller interface {
	Ask(ctx context.Context, in AskInput) (AskOutput, error)
}

// AskInput is the typed input to one LLM turn.
type AskInput struct {
	SystemPrompt    string
	UserMessage     string
	ToolAllowlist   []string
	Cwd             string
	ClaudeSessionID string
}

// AskOutput is the typed output from one LLM turn.
type AskOutput struct {
	Reply              string
	NewClaudeSessionID string
}

// Enter resolves modeName against the AppDef, opens or resumes the
// backing chat, and returns a Session bound to the captured Snapshot.
// The orchestrator is not touched — this is overlay-only by design
// (see types.go locked decisions).
func (c *Controller) Enter(ctx context.Context, snap Snapshot, modeName string) (*Session, error) {
	if c == nil {
		return nil, fmt.Errorf("metamode.Enter: nil controller")
	}
	if c.AppDef == nil {
		return nil, fmt.Errorf("metamode.Enter: nil AppDef")
	}
	if c.Agents == nil {
		return nil, fmt.Errorf("metamode.Enter: nil agent registry")
	}
	if c.Chats == nil {
		return nil, fmt.Errorf("metamode.Enter: nil chat store")
	}

	mode, ok := c.AppDef.MetaModes[modeName]
	if !ok || mode == nil {
		return nil, fmt.Errorf("metamode.Enter: unknown mode %q", modeName)
	}

	agent, ok := c.Agents.Get(mode.Agent)
	if !ok {
		return nil, fmt.Errorf("metamode.Enter: unknown agent %q (referenced by mode %q)", mode.Agent, modeName)
	}

	// Cwd resolution happens per-turn in Send (so the app-file fallback
	// can pick up the TurnContext). Enter just builds the Session;
	// cwd is not stashed here.

	// Snapshot the entry time if the caller didn't pre-fill it. This
	// lets Enter be called with just (state, world) and have the
	// controller stamp EnteredAt deterministically.
	if snap.EnteredAt.IsZero() {
		snap.EnteredAt = c.now()
	}

	room := metaRoom(modeName)
	scopeKey := metaScopeKey(modeName, string(snap.State))
	title := mode.Label
	if title == "" {
		title = modeName
	}

	chat, err := c.Chats.ResolveMeta(ctx, metaAppID(modeName, c.AppDef.App.ID), room, scopeKey, title)
	if err != nil {
		return nil, fmt.Errorf("metamode.Enter: resolve chat: %w", err)
	}

	return &Session{
		Mode:     mode,
		Agent:    agent,
		Chat:     chat,
		Snapshot: snap,
		Ledger:   c.newLedger(snap.SessionID),
	}, nil
}

// ChatListing is one row in the /meta list output. ModeName is parsed
// from Room ("meta:foo" → "foo"); FirstUserMessage is truncated to
// 100 chars (empty if no user turn yet).
type ChatListing struct {
	ID               string
	ModeName         string
	ScopeKey         string
	Title            string
	UpdatedAt        time.Time
	FirstUserMessage string
}

// firstUserMessageMaxLen bounds the FirstUserMessage preview surfaced
// in /meta list. 100 chars is enough to disambiguate at-a-glance
// without wrapping in narrow terminals.
const firstUserMessageMaxLen = 100

// ListChats returns one ChatListing per meta-chat in the app, sorted
// by UpdatedAt desc. Archived rows are excluded by the underlying
// ChatStore.ListMeta. Non-meta rooms are filtered defensively even
// though ListMeta should already exclude them.
//
// Cross-app `self` chats (keyed under SelfAppID) are merged into the
// result when the caller asks for any app other than SelfAppID
// itself — so `/meta list` inside a running app surfaces ongoing
// kitsoki-engineering conversations alongside the app's own chats.
// Callers that explicitly want only one bucket pass SelfAppID
// directly to see just the cross-app chats.
func (c *Controller) ListChats(ctx context.Context, appID string) ([]ChatListing, error) {
	if c == nil {
		return nil, fmt.Errorf("metamode.ListChats: nil controller")
	}
	if c.Chats == nil {
		return nil, fmt.Errorf("metamode.ListChats: nil chat store")
	}
	handles, err := c.Chats.ListMeta(ctx, appID)
	if err != nil {
		return nil, fmt.Errorf("metamode.ListChats: %w", err)
	}
	// Pull cross-app `self` chats alongside the app's own — but only
	// when the caller isn't already asking for SelfAppID (avoid the
	// double-list).
	if appID != SelfAppID {
		selfHandles, err := c.Chats.ListMeta(ctx, SelfAppID)
		if err != nil {
			return nil, fmt.Errorf("metamode.ListChats: self: %w", err)
		}
		handles = append(handles, selfHandles...)
	}
	out := make([]ChatListing, 0, len(handles))
	for _, h := range handles {
		if h == nil {
			continue
		}
		room := h.Room()
		if !strings.HasPrefix(room, "meta:") {
			continue
		}
		modeName := strings.TrimPrefix(room, "meta:")
		preview, perr := h.FirstUserMessage()
		if perr != nil {
			// Listing is best-effort; surface an empty preview
			// rather than fail the whole call when one row's
			// transcript read errors.
			preview = ""
		}
		preview = truncatePreview(preview, firstUserMessageMaxLen)
		out = append(out, ChatListing{
			ID:               h.ID(),
			ModeName:         modeName,
			ScopeKey:         h.ScopeKey(),
			Title:            h.Title(),
			UpdatedAt:        h.UpdatedAt(),
			FirstUserMessage: preview,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// EnterByChatID resumes an existing meta-mode chat by its full ID.
// Errors if the chat doesn't exist, isn't a meta chat, belongs to a
// different app, or the supplied modeName doesn't match the chat's
// "meta:<modeName>" room. On success it returns a Session shaped
// identically to Enter (same Agent resolution, fresh ProposalLedger).
func (c *Controller) EnterByChatID(ctx context.Context, snap Snapshot, modeName, chatID string) (*Session, error) {
	if c == nil {
		return nil, fmt.Errorf("metamode.EnterByChatID: nil controller")
	}
	if c.AppDef == nil {
		return nil, fmt.Errorf("metamode.EnterByChatID: nil AppDef")
	}
	if c.Agents == nil {
		return nil, fmt.Errorf("metamode.EnterByChatID: nil agent registry")
	}
	if c.Chats == nil {
		return nil, fmt.Errorf("metamode.EnterByChatID: nil chat store")
	}

	mode, ok := c.AppDef.MetaModes[modeName]
	if !ok || mode == nil {
		return nil, fmt.Errorf("metamode.EnterByChatID: unknown mode %q", modeName)
	}
	agent, ok := c.Agents.Get(mode.Agent)
	if !ok {
		return nil, fmt.Errorf("metamode.EnterByChatID: unknown agent %q (referenced by mode %q)", mode.Agent, modeName)
	}

	chat, err := c.Chats.GetMeta(ctx, chatID)
	if err != nil {
		return nil, fmt.Errorf("metamode.EnterByChatID: get chat: %w", err)
	}
	if chat == nil {
		return nil, fmt.Errorf("metamode.EnterByChatID: chat %q not found", chatID)
	}
	room := chat.Room()
	if !strings.HasPrefix(room, "meta:") {
		return nil, fmt.Errorf("metamode.EnterByChatID: chat %q is not a meta chat (room=%q)", chatID, room)
	}
	// `self` chats key against the synthetic SelfAppID across all apps
	// (proposal §1 cross-app keying). Allow them to resume from any
	// running app; reject only when the chat's app_id matches neither
	// the running app nor SelfAppID.
	if chat.AppID() != c.AppDef.App.ID && chat.AppID() != SelfAppID {
		return nil, fmt.Errorf("metamode.EnterByChatID: chat %q belongs to app %q, not %q",
			chatID, chat.AppID(), c.AppDef.App.ID)
	}
	if got := strings.TrimPrefix(room, "meta:"); got != modeName {
		return nil, fmt.Errorf("metamode.EnterByChatID: mode mismatch — chat is %q, requested %q", got, modeName)
	}

	if snap.EnteredAt.IsZero() {
		snap.EnteredAt = c.now()
	}

	return &Session{
		Mode:     mode,
		Agent:    agent,
		Chat:     chat,
		Snapshot: snap,
		Ledger:   c.newLedger(snap.SessionID),
	}, nil
}

// NewChat archives the active session's chat and opens a fresh one
// for the same (mode, scope). The returned Session points at the new
// chat row and a fresh ProposalLedger; the prior session's chat row
// persists in archived state and can still be resumed by ID through
// /meta resume.
func (c *Controller) NewChat(ctx context.Context, s *Session) (*Session, error) {
	if c == nil {
		return nil, fmt.Errorf("metamode.NewChat: nil controller")
	}
	if s == nil {
		return nil, fmt.Errorf("metamode.NewChat: nil session")
	}
	if s.Chat == nil {
		return nil, fmt.Errorf("metamode.NewChat: session has no chat handle")
	}
	if c.Chats == nil {
		return nil, fmt.Errorf("metamode.NewChat: nil chat store")
	}
	if c.AppDef == nil {
		return nil, fmt.Errorf("metamode.NewChat: nil AppDef")
	}

	oldID := s.Chat.ID()
	room := s.Chat.Room()
	scopeKey := s.Chat.ScopeKey()
	appID := s.Chat.AppID()

	if err := c.Chats.ArchiveMeta(ctx, oldID); err != nil {
		return nil, fmt.Errorf("metamode.NewChat: archive previous: %w", err)
	}

	// Title: prefer the prior mode's Label so the new chat surfaces
	// the same human label in listings. Fall back to the room name.
	title := room
	if s.Mode != nil && s.Mode.Label != "" {
		title = s.Mode.Label
	}

	fresh, err := c.Chats.ResolveMeta(ctx, appID, room, scopeKey, title)
	if err != nil {
		return nil, fmt.Errorf("metamode.NewChat: resolve fresh: %w", err)
	}
	if fresh.ID() == oldID {
		return nil, fmt.Errorf("metamode.NewChat: resolve returned archived chat %q — store did not skip archived rows", oldID)
	}

	return &Session{
		Mode:     s.Mode,
		Agent:    s.Agent,
		Chat:     fresh,
		Snapshot: s.Snapshot,
		Ledger:   c.newLedger(s.Snapshot.SessionID),
	}, nil
}

// ResolveChatIDPrefix returns the full chat ID matching prefix in the
// app's meta chats. Errors with an ErrAmbiguousPrefix-shaped message
// when more than one row matches; errors with "no match" when none do.
// Requires prefix length ≥ 3 to keep the user from typing one char and
// stumbling into the wrong chat.
func (c *Controller) ResolveChatIDPrefix(ctx context.Context, appID, prefix string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("metamode.ResolveChatIDPrefix: nil controller")
	}
	if len(prefix) < 3 {
		return "", fmt.Errorf("metamode.ResolveChatIDPrefix: prefix %q too short (need ≥3 chars)", prefix)
	}
	listings, err := c.ListChats(ctx, appID)
	if err != nil {
		return "", err
	}
	var matches []ChatListing
	for _, l := range listings {
		if strings.HasPrefix(l.ID, prefix) {
			matches = append(matches, l)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("metamode.ResolveChatIDPrefix: no chat matches prefix %q", prefix)
	case 1:
		return matches[0].ID, nil
	default:
		ids := make([]string, 0, len(matches))
		for _, m := range matches {
			ids = append(ids, m.ID)
		}
		return "", &AmbiguousPrefixError{Prefix: prefix, Matches: ids}
	}
}

// AmbiguousPrefixError is returned by ResolveChatIDPrefix when more
// than one chat ID shares the given prefix. The TUI uses the typed
// shape to render a disambiguation list to the user.
type AmbiguousPrefixError struct {
	Prefix  string
	Matches []string
}

func (e *AmbiguousPrefixError) Error() string {
	return fmt.Sprintf("ambiguous prefix %q matched %d chats: %s",
		e.Prefix, len(e.Matches), strings.Join(e.Matches, ", "))
}

// truncatePreview shortens s to max runes, returning the original
// when shorter. Operates on runes so multibyte text doesn't get cut
// mid-codepoint.
func truncatePreview(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// Send issues one turn on the session: appends the user message,
// dispatches to the oracle, persists the new claude session id, then
// appends the assistant reply.
//
// turn carries the per-turn ambient context (state path, app file,
// rendered view, world snapshot). The controller prepends a [context]
// block built from those fields to the user message before handing it
// to the oracle. When turn is the zero value, the preamble is empty
// and Send behaves like the old (turn-less) signature did.
//
// Ordering rationale: SetClaudeSessionID runs BEFORE the assistant
// append so a write failure on the session id can't strand an answered
// turn with no resume path. AppendMessage("user") happens FIRST so a
// later oracle failure still leaves the user's question visible in the
// transcript. The same ordering pattern is used by
// host.runOracleAskWithMCPWithChat (see oracle_ask_with_mcp.go) so
// transcripts stay consistent with the orchestrator-driven path.
//
// WS-A4 wraps this method (or composes it) to set ReloadRequested
// when an authoring.apply tool call lands during the turn. WS-A3
// always returns ReloadRequested:false.
func (c *Controller) Send(ctx context.Context, s *Session, userText string, turn TurnContext) (SendResult, error) {
	if c == nil {
		return SendResult{}, fmt.Errorf("metamode.Send: nil controller")
	}
	if s == nil {
		return SendResult{}, fmt.Errorf("metamode.Send: nil session")
	}
	if s.Chat == nil {
		return SendResult{}, fmt.Errorf("metamode.Send: session has no chat handle")
	}
	if c.Oracle == nil {
		return SendResult{}, fmt.Errorf("metamode.Send: nil oracle caller")
	}

	mode := ""
	if s.Mode != nil {
		mode = s.Mode.Trigger
	}
	slog.InfoContext(ctx, "metamode.send.start",
		"chat_id", s.Chat.ID(),
		"mode", mode,
		"agent", s.Agent.Name,
		"state", turn.StatePath,
		"user_chars", len(userText),
	)

	chatID := s.Chat.ID()

	// Acquire the per-chat singleton lock. Held across the user
	// append, oracle dispatch, and assistant append — every other
	// driver (kitsoki chat continue, the drive dispatcher,
	// kitsoki chat attach) acquires the same lock, so meta turns
	// can't interleave with them against the same row. On contention
	// we surface ErrChatBusy so the TUI renders a friendly message
	// rather than a generic oracle-error wrapper.
	var (
		result   SendResult
		innerErr error
	)
	lockErr := c.Chats.WithLock(ctx, chatID, func(lockedCtx context.Context) error {
		result, innerErr = c.sendLocked(lockedCtx, s, userText, turn, chatID, mode)
		return innerErr
	})
	if lockErr != nil {
		if errors.Is(lockErr, ErrChatBusy) {
			return SendResult{Err: lockErr}, lockErr
		}
		// innerErr is also returned by sendLocked; prefer the
		// already-wrapped form when WithLock surfaced it. WithLock
		// returns whatever fn returned, so when innerErr != nil it
		// equals lockErr here.
		if innerErr != nil {
			return result, innerErr
		}
		return SendResult{Err: lockErr}, lockErr
	}
	return result, nil
}

// sendLocked is the original Send body, factored out so the chat-lock
// callback in Send can hold it cleanly. ctx here is the locked
// context — short-lived helpers (heartbeats) that ride on the lock
// would attach to it, but meta-mode does not need a heartbeat goroutine
// since oracle.Ask is a one-shot call.
func (c *Controller) sendLocked(ctx context.Context, s *Session, userText string, turn TurnContext, chatID, mode string) (SendResult, error) {
	if err := s.Chat.AppendMessage("user", userText); err != nil {
		slog.ErrorContext(ctx, "metamode.send.append_user_failed",
			"chat_id", chatID, "err", err.Error())
		return SendResult{Err: err}, fmt.Errorf("metamode.Send: append user: %w", err)
	}

	// Compose the agent-facing user message: [context]…[/context]
	// preamble (built from turn) + [user]…[/user] block (the literal
	// text). The chat transcript above stores only the literal text so
	// the persisted history stays clean; the preamble is a per-turn
	// derived artefact, not user-authored.
	agentUserMessage := renderTurnContextPreamble(turn) + "[user]\n" + userText + "\n[/user]\n"

	in := AskInput{
		SystemPrompt:    s.Agent.SystemPrompt,
		UserMessage:     agentUserMessage,
		ToolAllowlist:   normaliseToolNames(s.Mode.Tools),
		Cwd:             resolveCwd(s.Mode, s.Agent, turn.AppFile),
		ClaudeSessionID: s.Chat.ClaudeSessionID(),
	}

	// Register the per-session ledger so the host-side
	// authoring.{propose,apply,discard} handlers can find it by
	// chat_id, if the agent still emits structured propose tokens.
	// The dispatcher is dormant when the agent uses direct file
	// edits (the current story-author protocol); the registration is
	// kept defensive for legacy chats that resume with old prompts.
	host.RegisterAuthoringLedger(chatID, ledgerAdapter{l: s.Ledger})
	defer host.UnregisterAuthoringLedger(chatID)

	// Snapshot the story directory tree before the LLM call so we can
	// detect direct edits to ANY file in the story (app.yaml, includes,
	// prompts, scripts) — not just the manifest — and trigger an
	// orchestrator reload + surface the change list on the way out.
	//
	// Imported-manifest dirs (proposal §16.4) are folded in as extra
	// roots so an edit in a sibling story (e.g. `stories/robbery/`
	// while running `stories/oregon-trail/`) is detected the same way.
	var (
		preStat  appFileStat
		preTree  storyTreeSnapshot
		treeRoot string
		extras   []string
	)
	if turn.AppFile != "" {
		treeRoot = filepath.Dir(turn.AppFile)
		extras = importedDirsFor(turn.ImportedManifestPaths)
		preTree = snapshotStoryTree(treeRoot, extras...)
	}
	preStat = statAppFile(turn.AppFile)

	slog.DebugContext(ctx, "metamode.oracle.ask",
		"chat_id", chatID,
		"cwd", in.Cwd,
		"tools", in.ToolAllowlist,
		"claude_session_id", in.ClaudeSessionID,
	)
	out, err := c.Oracle.Ask(ctx, in)
	if err != nil {
		slog.ErrorContext(ctx, "metamode.oracle.error",
			"chat_id", chatID,
			"mode", mode,
			"err", err.Error(),
		)
		return SendResult{Err: err}, fmt.Errorf("metamode.Send: oracle ask: %w", err)
	}
	slog.DebugContext(ctx, "metamode.oracle.reply",
		"chat_id", chatID,
		"reply_chars", len(out.Reply),
		"new_claude_session_id", out.NewClaudeSessionID,
	)

	// Dormant safety net: if a legacy chat still emits the structured
	// propose/apply tokens, parse and dispatch them so the resumed
	// chat keeps working. Modern chats won't trigger any of this
	// because the prompt no longer documents the protocol.
	out.Reply = c.dispatchAuthoringCalls(ctx, chatID, out.Reply, turn)

	if out.NewClaudeSessionID != "" && out.NewClaudeSessionID != s.Chat.ClaudeSessionID() {
		if err := s.Chat.SetClaudeSessionID(out.NewClaudeSessionID); err != nil {
			return SendResult{Err: err}, fmt.Errorf("metamode.Send: persist claude session id: %w", err)
		}
	}

	if err := s.Chat.AppendMessage("assistant", out.Reply); err != nil {
		slog.ErrorContext(ctx, "metamode.send.append_assistant_failed",
			"chat_id", chatID, "err", err.Error())
		return SendResult{Err: err}, fmt.Errorf("metamode.Send: append assistant: %w", err)
	}

	// Reload trigger #1 (modern): the agent edited ANY file in the story
	// directory tree (app.yaml, an include, a prompt, a script…) — or
	// in any imported child story's directory (proposal §16.4).
	// Reload trigger #2 (legacy): the ledger flipped during the
	// structured-token dispatch above. Either is sufficient.
	var changedFiles []string
	if treeRoot != "" {
		postTree := snapshotStoryTree(treeRoot, extras...)
		changedFiles = storyTreeChanges(preTree, postTree)
	}
	_ = preStat // kept for symmetry with the legacy single-file diagnostic
	reload := s.Ledger.ConsumeReload() || len(changedFiles) > 0
	slog.InfoContext(ctx, "metamode.send.done",
		"chat_id", chatID,
		"reload_requested", reload,
		"changed_files", changedFiles,
	)

	return SendResult{
		Assistant:       out.Reply,
		ChatID:          chatID,
		ReloadRequested: reload,
		ChangedFiles:    changedFiles,
		Err:             nil,
	}, nil
}

// appFileStat captures the mtime + size of one file so direct edits
// between two oracle calls can be detected. Zero value means "no file"
// (path empty or stat failed).
type appFileStat struct {
	exists bool
	mtime  time.Time
	size   int64
}

func statAppFile(path string) appFileStat {
	if path == "" {
		return appFileStat{}
	}
	info, err := osStat(path)
	if err != nil {
		return appFileStat{}
	}
	return appFileStat{exists: true, mtime: info.ModTime(), size: info.Size()}
}

// storyTreeSnapshot is a fingerprint of every file in the story
// directory subtree that the agent might edit: YAML manifests + included
// fragments, prompt templates, script files. The map key is the path
// relative to the root (stable across calls); the value carries
// mtime + size. Errors during the walk are folded into the map (we
// take whatever stats we could get); a totally unreadable root yields
// an empty map which means "no signal" — reload is not triggered.
type storyTreeSnapshot map[string]appFileStat

// importedDirsFor returns the unique parent directories of the given
// imported-manifest paths. Used by the controller's reload watcher
// to fold sibling stories into its file-snapshot tree.
//
// Duplicate dirs are collapsed (two imports from `stories/robbery/`
// at different aliases yield one watched dir). Empty / unstatable
// paths are silently dropped.
func importedDirsFor(manifestPaths []string) []string {
	if len(manifestPaths) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(manifestPaths))
	var out []string
	for _, p := range manifestPaths {
		if p == "" {
			continue
		}
		dir := filepath.Dir(p)
		if _, dup := seen[dir]; dup {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	return out
}

// snapshotStoryTree walks rootDir (typically filepath.Dir(turn.AppFile))
// and returns mtime + size for every file under it. Hidden dirs (.git,
// .worktrees, …) are skipped so commit churn doesn't flap the
// reload-detection. Symlinks are not followed.
//
// extraRoots is the optional list of additional directories to fold in:
// every imported manifest's directory (story-imports proposal §16.4),
// so an edit in a sibling story (`stories/robbery/` while running
// `stories/oregon-trail/`) triggers reload. Each extra root is walked
// the same way as the main root. Keys in the returned snapshot are
// prefixed with the absolute path so two roots with same-named files
// (`prompts/foo.md`) don't collide.
func snapshotStoryTree(rootDir string, extraRoots ...string) storyTreeSnapshot {
	out := storyTreeSnapshot{}
	if rootDir != "" {
		walkOneRoot(rootDir, "", out)
	}
	// Skip extra roots that nest under rootDir (they'll be walked
	// already), and dedupe siblings that share the same canonical path.
	seen := map[string]struct{}{}
	if rootDir != "" {
		if abs, err := filepath.Abs(rootDir); err == nil {
			seen[abs] = struct{}{}
		}
	}
	for _, extra := range extraRoots {
		if extra == "" {
			continue
		}
		abs, err := filepath.Abs(extra)
		if err != nil {
			continue
		}
		if _, dup := seen[abs]; dup {
			continue
		}
		// If extra is inside rootDir, walkOneRoot of rootDir already
		// covered it.
		if rootDir != "" {
			rootAbs, _ := filepath.Abs(rootDir)
			if rootAbs != "" && strings.HasPrefix(abs+string(filepath.Separator), rootAbs+string(filepath.Separator)) {
				seen[abs] = struct{}{}
				continue
			}
		}
		seen[abs] = struct{}{}
		// Key prefix = "@@abs@@" so the relative paths inside this
		// extra root don't collide with the main tree's keys.
		walkOneRoot(extra, "@@"+abs+"@@", out)
	}
	return out
}

// walkOneRoot is the per-root subroutine for snapshotStoryTree. The
// `keyPrefix` is prepended to every map key so multiple roots can share
// the same snapshot without filename collisions.
func walkOneRoot(rootDir, keyPrefix string, out storyTreeSnapshot) {
	_ = filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != rootDir && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(rootDir, path)
		if relErr != nil {
			rel = path
		}
		out[keyPrefix+rel] = appFileStat{exists: true, mtime: info.ModTime(), size: info.Size()}
		return nil
	})
}

// storyTreeChanges returns the relative paths whose stats differ
// between two snapshots. Sorted for deterministic output. An entry
// missing from either side counts as a change (created or deleted).
func storyTreeChanges(pre, post storyTreeSnapshot) []string {
	seen := make(map[string]struct{}, len(pre)+len(post))
	var changed []string
	for k, prev := range pre {
		seen[k] = struct{}{}
		next, ok := post[k]
		if !ok {
			changed = append(changed, displayKey(k)) // deleted
			continue
		}
		if prev.size != next.size || !prev.mtime.Equal(next.mtime) {
			changed = append(changed, displayKey(k))
		}
	}
	for k := range post {
		if _, already := seen[k]; already {
			continue
		}
		changed = append(changed, displayKey(k)) // created
	}
	sort.Strings(changed)
	return changed
}

// displayKey strips the multi-root snapshot's `@@<abs>@@` key prefix
// (added by walkOneRoot to keep sibling roots disjoint) and re-attaches
// the imported story's base directory name so a changed file in
// `stories/robbery/prompts/intro.md` renders as
// `robbery/prompts/intro.md` rather than the raw absolute path or a
// bare `prompts/intro.md` that's indistinguishable from the main
// story's own prompts. Keys without the prefix pass through unchanged.
func displayKey(k string) string {
	if !strings.HasPrefix(k, "@@") {
		return k
	}
	end := strings.Index(k[2:], "@@")
	if end < 0 {
		return k
	}
	absRoot := k[2 : 2+end]
	rel := k[2+end+2:]
	base := filepath.Base(absRoot)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return rel
	}
	return base + string(filepath.Separator) + rel
}

// dispatchAuthoringCalls scans the assistant reply for the Path-Y
// structured authoring tokens (see host/authoring_tools.go for the
// grammar), invokes the matching in-process handler for each, and
// rewrites the reply by replacing each token with a short result
// summary so the user sees what happened. Errors are captured inline
// rather than aborting — the agent can recover on the next turn.
//
// turn.AppFile (if set) is used as a defense-in-depth default when
// the agent's propose payload omits app_file. CurrentState /
// CurrentView in the payload are likewise auto-filled from
// turn.StatePath / turn.RenderedView when the agent left them blank,
// so the editing sub-agent always sees the same per-turn context the
// story-author received.
func (c *Controller) dispatchAuthoringCalls(ctx context.Context, chatID, reply string, turn TurnContext) string {
	calls := host.ParseAuthoringCalls(reply)
	if len(calls) == 0 {
		return reply
	}

	rewriter := newReplyRewriter(reply)
	for _, call := range calls {
		switch call.Kind {
		case host.AuthoringCallPropose:
			var args host.AuthoringProposeArgs
			if err := json.Unmarshal([]byte(call.Payload), &args); err != nil {
				rewriter.replaceFirstProposeBlock(fmt.Sprintf("[authoring.propose error: invalid JSON payload: %v]", err))
				continue
			}
			args.ChatID = chatID
			// Defense-in-depth: auto-fill app_file / current_state /
			// current_view from the TurnContext when the agent omits
			// them. The prompt asks the agent to set them; this guards
			// against a forgetful agent strand.
			if strings.TrimSpace(args.AppFile) == "" {
				args.AppFile = turn.AppFile
			}
			if strings.TrimSpace(args.CurrentState) == "" {
				args.CurrentState = turn.StatePath
			}
			if strings.TrimSpace(args.CurrentView) == "" {
				args.CurrentView = turn.RenderedView
			}
			out, err := host.AuthoringPropose(ctx, args)
			if err != nil {
				rewriter.replaceFirstProposeBlock(fmt.Sprintf("[authoring.propose error: %v]", err))
				continue
			}
			rewriter.replaceFirstProposeBlock(fmt.Sprintf("[proposal %s drafted: %s]", out.ProposalID, out.Summary))
		case host.AuthoringCallApply:
			out, err := host.AuthoringApply(ctx, host.AuthoringApplyArgs{ChatID: chatID, ProposalID: call.ProposalID})
			if err != nil {
				rewriter.replaceApplyToken(call.ProposalID, fmt.Sprintf("[authoring.apply %s error: %v]", call.ProposalID, err))
				continue
			}
			rewriter.replaceApplyToken(call.ProposalID, fmt.Sprintf("[proposal %s applied: %s]", call.ProposalID, out.Summary))
		case host.AuthoringCallDiscard:
			out, err := host.AuthoringDiscard(ctx, host.AuthoringDiscardArgs{ChatID: chatID, ProposalID: call.ProposalID})
			if err != nil {
				rewriter.replaceDiscardToken(call.ProposalID, fmt.Sprintf("[authoring.discard %s error: %v]", call.ProposalID, err))
				continue
			}
			_ = out
			rewriter.replaceDiscardToken(call.ProposalID, fmt.Sprintf("[proposal %s discarded]", call.ProposalID))
		}
	}
	return rewriter.String()
}

// Exit finalizes a meta-mode session.
//
// Disposition of pending proposals (decision flagged in the WS-A3
// report): the meta-mode proposal §6.4 explicitly says that on
// reentry the chat resumes "with that proposal still draft", so
// drafts MUST survive Exit when the mode is persistent. We therefore
// touch the ledger nothing in the nominal case — drafts remain so the
// next Enter (which re-resolves the same chat for the same state) can
// pick them up. Truly orphan proposals (the rare case where a Propose
// call produced a shadow dir but the LLM crashed mid-turn before
// recording it in the ledger) are out of scope for this method: by
// definition they aren't in the ledger.
//
// Ephemeral modes (mode.Persist == false): when the author opts out
// of persistence, Exit archives the backing chat so it stops showing
// up in /meta list and the next Enter for the same (mode, scope) mints
// a fresh row. The transcript itself is preserved (Archive is a soft
// status change) so resume-by-ID still works for forensic reads, but
// the active surface treats the conversation as discarded. Persist:true
// (the default) keeps the chat exactly as before — no archive on Exit.
func (c *Controller) Exit(ctx context.Context, s *Session) error {
	if c == nil || s == nil {
		return nil
	}
	if s.Mode == nil || s.Mode.PersistOrDefault() {
		return nil
	}
	if s.Chat == nil || c.Chats == nil {
		return nil
	}
	if err := c.Chats.ArchiveMeta(ctx, s.Chat.ID()); err != nil {
		return fmt.Errorf("metamode.Exit: archive ephemeral chat: %w", err)
	}
	return nil
}

// Done archives the active session's chat and returns its ID. Unlike
// Exit (which only archives when the mode is non-persistent) and
// NewChat (which archives and opens a fresh row), Done is the
// user-signalled "I'm finished with this chat — don't keep it around"
// path. The chat goes to archived state so it no longer shows up in
// the default /meta list / sessions-panel listing; it can still be
// resumed by full ID via /meta resume for forensic reads.
//
// Persist:false modes call ArchiveMeta from Exit too, so Done is
// mostly useful for the default persist:true case. Calling Done
// twice in a row is safe — the second call hits the same archived
// row and returns the same ID without erroring (ArchiveMeta is
// idempotent at the SQLite layer).
//
// Returns the archived chat ID for the caller's confirmation
// message.
func (c *Controller) Done(ctx context.Context, s *Session) (string, error) {
	if c == nil {
		return "", fmt.Errorf("metamode.Done: nil controller")
	}
	if s == nil || s.Chat == nil {
		return "", fmt.Errorf("metamode.Done: no active session")
	}
	if c.Chats == nil {
		return "", fmt.Errorf("metamode.Done: nil chat store")
	}
	id := s.Chat.ID()
	if err := c.Chats.ArchiveMeta(ctx, id); err != nil {
		return "", fmt.Errorf("metamode.Done: archive %s: %w", id, err)
	}
	return id, nil
}

// now is the clock accessor with a sane default.
func (c *Controller) now() time.Time {
	if c.Clock != nil {
		return c.Clock()
	}
	return time.Now()
}

// SessionWorkspace returns the cwd the controller would pass to a
// new turn for sess against appFile. Exposed so the TUI's /attach
// path can spawn `claude --resume` in the same directory the typed
// /meta flow uses, keeping file-edit behaviour consistent across the
// two modes.
func SessionWorkspace(sess *Session, appFile string) string {
	if sess == nil || sess.Mode == nil {
		return ""
	}
	return resolveCwd(sess.Mode, sess.Agent, appFile)
}

// resolveCwd picks the cwd for an Ask, returning an absolute path
// whenever a non-empty value is selected. Precedence:
//
//  1. mode.Cwd     — the meta_mode's explicit `cwd:` field.
//  2. agent.DefaultCwd — fallback when no mode override is set.
//  3. filepath.Dir(appFile) — last-resort fallback so a /meta story
//     conversation sees the whole app tree without each app having
//     to declare cwd: explicitly.
//
// Any selected value that is not already absolute is resolved with
// filepath.Abs. For relative paths in cases (1) and (2) we resolve
// against the directory of appFile when known (so author-written
// `cwd: ./foo` makes sense relative to the app file), falling back
// to the process cwd via filepath.Abs. The whole point is that the
// returned string is safe to hand to tmux's `-c` flag: tmux applies
// start_directory against the inherited working directory, which
// the kitsoki TUI does NOT control, so a relative path can land the
// pane in $HOME — the user-witnessed bug.
//
// Returns "" only when all three precedence sources are empty.
func resolveCwd(m *app.MetaModeDef, a agents.Agent, appFile string) string {
	// Pre-compute the app-file directory (absolute when possible) so
	// each branch can lean on it for relative-path resolution without
	// re-doing the filepath.Abs dance.
	var appDirAbs string
	if appFile != "" {
		if abs, err := filepath.Abs(appFile); err == nil {
			appDirAbs = filepath.Dir(abs)
		} else {
			appDirAbs = filepath.Dir(appFile)
		}
	}
	switch {
	case m != nil && m.Cwd != "":
		return absolutiseAgainst(m.Cwd, appDirAbs)
	case a.DefaultCwd != "":
		return absolutiseAgainst(a.DefaultCwd, appDirAbs)
	case appDirAbs != "":
		// The app-file fallback is already absolute (we ran
		// filepath.Abs on appFile above). Clean for tidy output.
		return filepath.Clean(appDirAbs)
	default:
		return ""
	}
}

// absolutiseAgainst returns an absolute form of raw. If raw is
// already absolute it is returned cleaned. Relative paths are
// resolved against baseDir when baseDir is non-empty (so author-
// written `cwd: ./includes` makes sense alongside the app yaml);
// otherwise they are resolved against the process cwd via
// filepath.Abs. If filepath.Abs fails (a real rarity — it only
// errors when the OS can't get the cwd), the original is returned
// rather than losing the value.
func absolutiseAgainst(raw, baseDir string) string {
	if raw == "" {
		return ""
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	if baseDir != "" {
		return filepath.Clean(filepath.Join(baseDir, raw))
	}
	if abs, err := filepath.Abs(raw); err == nil {
		return abs
	}
	return raw
}

// metaRoom produces the chat-room key for a meta mode by name.
// "meta:<modeName>" matches the convention in the meta-mode proposal
// §3.1 step 3 and the existing `kitsoki chat list --scope-prefix meta:`
// listing path.
func metaRoom(modeName string) string { return "meta:" + modeName }

// newLedger creates a ProposalLedger and, when c.JournalWriter is non-nil,
// wires the writer and session ID into the ledger for continue-mode
// journal writes (§4.7 v3). This centralises the wiring so Enter,
// EnterByChatID, and NewChat all get the same treatment.
func (c *Controller) newLedger(sid app.SessionID) *ProposalLedger {
	l := NewProposalLedger()
	if c.JournalWriter != nil {
		l.WithLedgerJournalWriter(c.JournalWriter, sid)
	}
	return l
}

// SelfAppID is the synthetic app_id under which kitsoki-target meta
// chats are stored. It is intentionally not a valid app YAML id (no app
// could declare `app.id: kitsoki-self` and collide), so chats keyed
// against it survive across every running app — a `kitsoki.edit`
// conversation started while playing cloak is the same row the user
// reopens while playing dev-story. Cross-app keying is the proposal §1
// design (option a).
const SelfAppID = "kitsoki-self"

// isKitsokiTargetMode reports whether modeName addresses kitsoki itself
// (the engine source / its own bug tracker) rather than the running
// app. These modes are cross-app: their chat rows are stored under
// SelfAppID so the conversation survives switching between stories.
//
// Covers both the new grouped keys (`kitsoki.edit`, `kitsoki.ask`,
// `kitsoki.bug`) and the legacy single-token `self` key for any
// in-flight back-compat callers — but the latter is no longer surfaced
// by the trigger parser per proposal §7 clean break.
func isKitsokiTargetMode(modeName string) bool {
	if modeName == "self" {
		return true
	}
	return strings.HasPrefix(modeName, "kitsoki.")
}

// metaAppID returns the app_id used to resolve a meta chat row for the
// given mode. For kitsoki-target modes it ignores the running app and
// returns SelfAppID so the conversation is cross-app; every other mode
// keys under the running app's id.
func metaAppID(modeName, runningAppID string) string {
	if isKitsokiTargetMode(modeName) {
		return SelfAppID
	}
	return runningAppID
}

// metaScopeKey returns the scope_key used to resolve a meta chat for
// the given mode. Kitsoki-target modes are cross-app, so the user's
// current state in their running app is irrelevant — one conversation
// per (user, kitsoki verb), period. Every other mode keys against the
// current state path so a chat opened in `bar.dark` is distinct from
// one opened in `foyer`.
func metaScopeKey(modeName, statePath string) string {
	if isKitsokiTargetMode(modeName) {
		return ""
	}
	return statePath
}

// ─── ledger adapter (avoids an import cycle metamode→host→metamode) ─────────
//
// host.AuthoringLedger / host.LedgerEntry are interfaces declared in
// the host package so the authoring handlers can mutate a per-session
// ledger without an import cycle. The two adapter types below bridge
// *ProposalLedger / *PendingProposal to those interfaces. The
// Controller registers a ledgerAdapter under the chat id before each
// Oracle.Ask and de-registers after.

// ledgerAdapter wraps *ProposalLedger as a host.AuthoringLedger.
type ledgerAdapter struct{ l *ProposalLedger }

func (a ledgerAdapter) Add(p *authoring.Proposal) string {
	return a.l.Add(p)
}

func (a ledgerAdapter) Get(id string) (host.LedgerEntry, bool) {
	pp, ok := a.l.Get(id)
	if !ok {
		return nil, false
	}
	return entryAdapter{pp: pp}, true
}

func (a ledgerAdapter) Discard(id string) error {
	return a.l.Discard(id)
}

func (a ledgerAdapter) RecordApplied(id string) {
	a.l.RecordApplied(id)
}

// entryAdapter wraps *PendingProposal as a host.LedgerEntry.
type entryAdapter struct{ pp *PendingProposal }

func (a entryAdapter) ProposalID() string              { return a.pp.ID }
func (a entryAdapter) Underlying() *authoring.Proposal { return a.pp.Proposal }

// Compile-time interface checks so a future field rename trips here
// rather than at runtime.
var (
	_ host.AuthoringLedger = ledgerAdapter{}
	_ host.LedgerEntry     = entryAdapter{}
)

// ─── per-turn context preamble ───────────────────────────────────────────────
//
// The TUI hands every Send call a TurnContext snapshot of the
// state-machine state, the path to the app.yaml on disk, the rendered
// view the user is staring at, and the resolved world variables. The
// preamble below glues those fields together into a single text block
// the controller prepends to the user message before dispatching to
// the oracle. The agent (story-author.md) is taught to read this
// preamble and use it to pin propose calls to the right file.
//
// Format choices:
//
//   - Single-bracket lowercase tags (`[context]`, `[user]`) rather than
//     XML, because Claude tends to over-interpret HTML/XML structure.
//   - Empty fields are omitted — no "state: \"\"" placeholder lines.
//   - `view` uses YAML literal block (`|`) so multi-line markdown
//     survives without escape gymnastics.
//   - World renders as YAML-ish key:value with two-space indent. Each
//     value is truncated at 200 chars to keep the preamble bounded.
//   - World keys are sorted lexicographically so the preamble is
//     deterministic (Go's map iteration order is random).

// turnContextWorldValueMaxLen bounds each rendered world-var value in
// the [context] preamble. 200 is enough to surface short strings and
// numbers without dumping multi-kilobyte slices.
const turnContextWorldValueMaxLen = 200

// renderTurnContextPreamble produces the [context]…[/context]\n\n
// prefix for a TurnContext. Returns "" when every field is empty.
func renderTurnContextPreamble(turn TurnContext) string {
	hasState := strings.TrimSpace(turn.StatePath) != ""
	hasAppFile := strings.TrimSpace(turn.AppFile) != ""
	hasView := strings.TrimSpace(turn.RenderedView) != ""
	hasWorld := len(turn.World) > 0
	hasTrace := strings.TrimSpace(turn.TracePath) != ""
	if !hasState && !hasAppFile && !hasView && !hasWorld && !hasTrace {
		return ""
	}

	var b strings.Builder
	b.WriteString("[context]\n")
	if hasState {
		b.WriteString("state: ")
		b.WriteString(turn.StatePath)
		b.WriteString("\n")
	}
	if hasAppFile {
		b.WriteString("app_file: ")
		b.WriteString(turn.AppFile)
		b.WriteString("\n")
	}
	if hasTrace {
		b.WriteString("trace_file: ")
		b.WriteString(turn.TracePath)
		b.WriteString("\n")
	}
	if hasView {
		b.WriteString("view: |\n")
		for _, line := range strings.Split(turn.RenderedView, "\n") {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}
	if hasWorld {
		b.WriteString("world:\n")
		keys := make([]string, 0, len(turn.World))
		for k := range turn.World {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString("  ")
			b.WriteString(k)
			b.WriteString(": ")
			b.WriteString(formatWorldValuePreview(turn.World[k]))
			b.WriteString("\n")
		}
	}
	b.WriteString("[/context]\n\n")
	return b.String()
}

// formatWorldValuePreview renders v as a single-line preview suitable
// for the [context] preamble. Strings are shown unquoted; other types
// are stringified with %v. Output is truncated to
// turnContextWorldValueMaxLen runes with a trailing "…" when cut.
func formatWorldValuePreview(v any) string {
	var s string
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		s = x
	default:
		s = fmt.Sprintf("%v", x)
	}
	// Collapse newlines so the preamble stays line-based — multi-line
	// strings in world vars are rare but should not break the preamble.
	s = strings.ReplaceAll(s, "\n", " ")
	runes := []rune(s)
	if len(runes) > turnContextWorldValueMaxLen {
		return string(runes[:turnContextWorldValueMaxLen]) + "…"
	}
	return s
}
