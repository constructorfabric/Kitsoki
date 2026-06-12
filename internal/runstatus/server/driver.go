package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/x/ansi"

	"kitsoki/internal/app"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/render/elements"
)

// Driver is the write side of the runstatus surface: the server calls it to
// advance one live session. It is nil for read-only surfaces (`kitsoki status
// serve`), in which case the write RPCs return codeReadOnly.
//
// The session id is bound by the implementation, not passed per call — the
// server serves a single session. Methods mirror the orchestrator's turn API:
//
//   - Turn routes free-form text through the harness (semantic → cache → LLM).
//   - SubmitDirect applies a chosen intent + slots with no routing (the choice
//     / confirmation path).
//   - ContinueTurn supplies missing slots for a pending clarification.
//   - AskOffPath runs a read-only off-path question that does not mutate state.
type Driver interface {
	Turn(ctx context.Context, input string) (*orchestrator.TurnOutcome, error)
	SubmitDirect(ctx context.Context, intent string, slots map[string]any) (*orchestrator.TurnOutcome, error)
	ContinueTurn(ctx context.Context, slots map[string]any) (*orchestrator.TurnOutcome, error)
	AskOffPath(ctx context.Context, input string) (string, error)
	// View returns a read-only TurnOutcome for the session's CURRENT state
	// (room render + allowed menu) without advancing it. It backs the
	// runstatus.session.view RPC the browser calls on load.
	View(ctx context.Context) (*orchestrator.TurnOutcome, error)
	// IntentInfo resolves one allowed intent name to its menu metadata
	// (title + single free-text slot binding) against the current state, so
	// newTurnResult can enrich the menu the browser renders. Returns the zero
	// value with ok=false when the name does not resolve.
	IntentInfo(name string, state string) (intentInfo, bool)
	// DefaultIntent returns the resolved name of the given state's free-text
	// sink (its `default_intent`), or "" when the state declares none. The
	// browser composer defaults its text-input box to this intent so a typed
	// reply routes the way the room author intended (e.g. `answer` in the PRD
	// clarifying room) instead of an arbitrary first text-slot intent.
	DefaultIntent(state string) string
	// PatchWorld injects world-key overrides into the session event log without
	// advancing a turn. For demo/test tooling (runstatus.session.patch_world).
	PatchWorld(ctx context.Context, patch map[string]any) error

	// ── Inbox (background-job notifications) ───────────────────────────────
	// These delegate to the session's [jobs.JobStore]. The nil-safety contract
	// mirrors internal/tui/inbox.go and Orchestrator.Teleport: a session with no
	// JobStore (headless tests, artifact mode) reports an empty inbox and the
	// mutating methods no-op rather than erroring.

	// ListNotifications returns the session's non-dismissed notifications,
	// newest first. Returns (nil, nil) when no JobStore is configured.
	ListNotifications(ctx context.Context) ([]jobs.Notification, error)
	// MarkNotificationRead stamps read_at on a notification. No-op without a
	// JobStore.
	MarkNotificationRead(ctx context.Context, id string) error
	// DismissNotification stamps dismissed_at, dropping the row from the inbox.
	// No-op without a JobStore.
	DismissNotification(ctx context.Context, id string) error
	// Teleport resolves a notification id to its [inbox.TeleportTarget] and jumps
	// the session there via Orchestrator.Teleport, restoring the saved slots.
	// Returns a typed error when there is no JobStore, the notification is
	// unknown, or it is not teleportable (empty target state) — the surface
	// renders such items read-only.
	Teleport(ctx context.Context, notificationID string) (*orchestrator.TurnOutcome, error)
}

// OrchestratorDriver adapts a live *orchestrator.Orchestrator + session id to
// the [Driver] interface by binding the session id, so the server can stay
// single-session and transport-only.
type OrchestratorDriver struct {
	Orch *orchestrator.Orchestrator
	SID  app.SessionID
	// Jobs is the session's notification store. It MAY be nil (headless tests,
	// artifact-mode, read-only surfaces); the inbox methods treat a nil store as
	// an empty inbox per the nil-safety contract on the [Driver] inbox methods.
	Jobs *jobs.JobStore
}

// ErrNoInbox is returned by Teleport when the session has no JobStore wired, so
// the surface can distinguish "no inbox here" from a genuine teleport failure.
var ErrNoInbox = errors.New("session has no inbox configured")

// ErrNotTeleportable is returned by Teleport when the notification exists but
// carries no destination state (an informational item); the surface renders it
// read-only rather than as a dead link.
var ErrNotTeleportable = errors.New("notification is not teleportable")

func (d OrchestratorDriver) Turn(ctx context.Context, input string) (*orchestrator.TurnOutcome, error) {
	return d.Orch.Turn(ctx, d.SID, input)
}

func (d OrchestratorDriver) SubmitDirect(ctx context.Context, intent string, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	return d.Orch.SubmitDirect(ctx, d.SID, intent, slots)
}

func (d OrchestratorDriver) ContinueTurn(ctx context.Context, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	return d.Orch.ContinueTurn(ctx, d.SID, slots)
}

func (d OrchestratorDriver) AskOffPath(ctx context.Context, input string) (string, error) {
	return d.Orch.AskOffPath(ctx, d.SID, input)
}

func (d OrchestratorDriver) View(ctx context.Context) (*orchestrator.TurnOutcome, error) {
	return d.Orch.CurrentView(ctx, d.SID)
}

func (d OrchestratorDriver) PatchWorld(ctx context.Context, patch map[string]any) error {
	return d.Orch.PatchWorld(ctx, d.SID, patch)
}

// HarnessController is the OPTIONAL harness-profile surface a Driver may expose:
// reading the declared profiles + live selection, and switching it. The server
// type-asserts a Driver to it for the runstatus.session.harness /
// set_selection RPCs; a Driver that doesn't implement it (read-only / artifact
// surfaces with no orchestrator) makes those RPCs report "no profiles". Kept
// off the core Driver interface so those surfaces need not implement it.
//
// SetHarnessSelection does NOT mutate the session journey (it sets in-memory,
// per-session selection consulted on the next dispatch), so unlike Turn it
// needs no writer lock — lockingDriver forwards it unlocked.
type HarnessController interface {
	HarnessProfiles() []orchestrator.ProfileInfo
	HarnessSelection() orchestrator.ProfileSelection
	SetHarnessSelection(profile, model string) error
}

func (d OrchestratorDriver) HarnessProfiles() []orchestrator.ProfileInfo {
	return d.Orch.Profiles()
}

func (d OrchestratorDriver) HarnessSelection() orchestrator.ProfileSelection {
	return d.Orch.Selection()
}

func (d OrchestratorDriver) SetHarnessSelection(profile, model string) error {
	return d.Orch.SetSelection(profile, model)
}

// ListNotifications returns the session's notifications newest-first, or an
// empty list when no JobStore is wired (nil-safety contract). The 0 limit asks
// the store for all non-dismissed rows.
func (d OrchestratorDriver) ListNotifications(ctx context.Context) ([]jobs.Notification, error) {
	if d.Jobs == nil {
		return nil, nil
	}
	return d.Jobs.ListNotifications(ctx, d.SID, 0)
}

// MarkNotificationRead is a no-op without a JobStore.
func (d OrchestratorDriver) MarkNotificationRead(ctx context.Context, id string) error {
	if d.Jobs == nil {
		return nil
	}
	return d.Jobs.MarkNotificationRead(ctx, id)
}

// DismissNotification is a no-op without a JobStore.
func (d OrchestratorDriver) DismissNotification(ctx context.Context, id string) error {
	if d.Jobs == nil {
		return nil
	}
	return d.Jobs.DismissNotification(ctx, id)
}

// Teleport resolves the notification, projects it to a [inbox.TeleportTarget],
// and delegates to Orchestrator.Teleport — the same deterministic jump the TUI
// and Oracle Room banner use, so the trace is indistinguishable from a TUI
// teleport. A nil JobStore returns [ErrNoInbox]; an unknown id or an empty
// destination state returns [ErrNotTeleportable].
func (d OrchestratorDriver) Teleport(ctx context.Context, notificationID string) (*orchestrator.TurnOutcome, error) {
	if d.Jobs == nil {
		return nil, ErrNoInbox
	}
	n, err := d.Jobs.GetNotification(ctx, notificationID)
	if err != nil {
		return nil, fmt.Errorf("teleport: resolve notification %q: %w", notificationID, err)
	}
	if n == nil {
		return nil, fmt.Errorf("teleport: %w: unknown notification %q", ErrNotTeleportable, notificationID)
	}
	target := inbox.FromNotification(*n)
	if target.State == "" {
		return nil, fmt.Errorf("teleport: %w: notification %q has no destination", ErrNotTeleportable, notificationID)
	}
	return d.Orch.Teleport(ctx, d.SID, target)
}

// IntentInfo resolves the intent's slot schema against `state` and derives the
// browser menu metadata. TextSlot is the single string slot the UI binds its
// input box to: an intent qualifies when it has exactly one string-typed slot
// AND no required slot of a different type (so `answer`, which needs an int `n`
// plus a string `text`, reports no TextSlot — it needs a form, not a text box).
func (d OrchestratorDriver) IntentInfo(name string, state string) (intentInfo, bool) {
	def, ok := d.Orch.LookupIntent(app.StatePath(state), name)
	if !ok {
		return intentInfo{}, false
	}
	info := intentInfo{
		Name:     name,
		Title:    def.Title,
		HasSlots: len(def.Slots) > 0,
	}
	var stringSlots []string
	requiredNonString := false
	for sname, sdef := range def.Slots {
		if sdef.Type == "string" {
			stringSlots = append(stringSlots, sname)
		} else if sdef.Required {
			requiredNonString = true
		}
	}
	if len(stringSlots) == 1 && !requiredNonString {
		info.TextSlot = stringSlots[0]
	}
	return info, true
}

// DefaultIntent returns the resolved free-text-sink intent name for the given
// state (or "" when none). See the Driver interface doc.
func (d OrchestratorDriver) DefaultIntent(state string) string {
	return d.Orch.StateDefaultIntent(app.StatePath(state))
}

// turnResult is the JSON wire shape for a [orchestrator.TurnOutcome]. It is the
// write-RPC response the SPA renders: the resolved typed view (so the browser
// can lay out elements itself), the pre-rendered text fallback, the allowed
// intents for the next menu, and — on a rejection or clarification — the
// structured reason. A guard rejection or missing slot is NOT a transport
// error: it rides back here as mode=rejected / mode=clarify, since it is a
// normal interpreted outcome of the turn. Only infra failures surface as an
// rpcError.
type turnResult struct {
	Mode           string    `json:"mode"`
	State          string    `json:"state"`
	View           string    `json:"view,omitempty"`
	TypedView      *app.View `json:"typed_view,omitempty"`
	AllowedIntents []string  `json:"allowed_intents,omitempty"`
	// Intents is the enriched menu the browser renders: one entry per allowed
	// intent, in AllowedIntents order, carrying the intent's title and the
	// single free-text slot (if any) the UI binds its input box to. It is the
	// structured companion to AllowedIntents (which stays for back-compat).
	Intents []intentInfo `json:"intents,omitempty"`
	// DefaultIntent is the resolved name of the current state's free-text sink
	// (its `default_intent`), or "" when none. The composer defaults its text
	// box to this intent. See Driver.DefaultIntent.
	DefaultIntent string                  `json:"default_intent,omitempty"`
	SlotsNeeded   []orchestrator.SlotNeed `json:"slots_needed,omitempty"`
	PendingIntent string                  `json:"pending_intent,omitempty"`
	PendingSlots  map[string]any          `json:"pending_slots,omitempty"`
	ErrorCode     string                  `json:"error_code,omitempty"`
	ErrorMessage  string                  `json:"error_message,omitempty"`
	GuardHint     string                  `json:"guard_hint,omitempty"`
	HarnessError  string                  `json:"harness_error,omitempty"`
	TurnNumber    int                     `json:"turn_number"`
}

// intentInfo is one entry in turnResult.Intents — the per-intent menu metadata
// the browser uses to label a button and bind a free-text input box.
type intentInfo struct {
	// Name is the intent name to submit (matches an AllowedIntents entry).
	Name string `json:"name"`
	// Title is the author-declared intent title (may be empty).
	Title string `json:"title,omitempty"`
	// TextSlot is the name of the single free-text/string slot the UI should
	// bind its input box to, or empty when the intent takes no free text (a
	// no-slot intent like `start`) or needs a multi-field form (like `answer`).
	TextSlot string `json:"text_slot,omitempty"`
	// HasSlots is true when the intent declares any slots at all.
	HasSlots bool `json:"has_slots"`
}

// newTurnResult flattens a TurnOutcome into the wire shape. resolver, when
// non-nil, enriches the allowed-intent menu into turnResult.Intents; it is the
// Driver, which resolves each name's slot schema against the outcome's state.
func newTurnResult(out *orchestrator.TurnOutcome, resolver Driver) turnResult {
	if out == nil {
		return turnResult{}
	}
	// Strip ANSI terminal codes from the text view — the browser cannot
	// render them. The TUI reads View off TurnOutcome directly (before this
	// function runs) so stripping here only affects the web response.
	plainView := ansi.Strip(out.View)

	// Pre-evaluate element Sources so the browser gets concrete text rather
	// than raw pongo templates (e.g. "Party of {{ world.party_size }}").
	// Falls back to nil when TypedView is unavailable or evaluation fails.
	var browserTypedView *app.View
	if out.TypedView != nil && len(out.TypedView.Elements) > 0 {
		if ev, err := elements.EvalElements(*out.TypedView, out.RenderEnv, out.Renderer); err == nil {
			browserTypedView = &ev
		}
	}

	tr := turnResult{
		Mode:           out.Mode.String(),
		State:          string(out.NewState),
		View:           plainView,
		TypedView:      browserTypedView,
		AllowedIntents: out.AllowedIntents,
		SlotsNeeded:    out.SlotsNeeded,
		PendingIntent:  out.PendingIntent,
		PendingSlots:   out.PendingSlots,
		ErrorCode:      string(out.ErrorCode),
		ErrorMessage:   out.ErrorMessage,
		GuardHint:      out.GuardHint,
		HarnessError:   out.HarnessError,
		TurnNumber:     int(out.TurnNumber),
	}
	if resolver != nil {
		for _, name := range out.AllowedIntents {
			if info, ok := resolver.IntentInfo(name, string(out.NewState)); ok {
				tr.Intents = append(tr.Intents, info)
			} else {
				tr.Intents = append(tr.Intents, intentInfo{Name: name})
			}
		}
		tr.DefaultIntent = resolver.DefaultIntent(string(out.NewState))
	}
	return tr
}
