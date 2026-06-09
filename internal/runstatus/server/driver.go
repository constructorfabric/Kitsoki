package server

import (
	"context"

	"github.com/charmbracelet/x/ansi"

	"kitsoki/internal/app"
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
	// PatchWorld injects world-key overrides into the session event log without
	// advancing a turn. For demo/test tooling (runstatus.session.patch_world).
	PatchWorld(ctx context.Context, patch map[string]any) error
}

// OrchestratorDriver adapts a live *orchestrator.Orchestrator + session id to
// the [Driver] interface by binding the session id, so the server can stay
// single-session and transport-only.
type OrchestratorDriver struct {
	Orch *orchestrator.Orchestrator
	SID  app.SessionID
}

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

// turnResult is the JSON wire shape for a [orchestrator.TurnOutcome]. It is the
// write-RPC response the SPA renders: the resolved typed view (so the browser
// can lay out elements itself), the pre-rendered text fallback, the allowed
// intents for the next menu, and — on a rejection or clarification — the
// structured reason. A guard rejection or missing slot is NOT a transport
// error: it rides back here as mode=rejected / mode=clarify, since it is a
// normal interpreted outcome of the turn. Only infra failures surface as an
// rpcError.
type turnResult struct {
	Mode           string                  `json:"mode"`
	State          string                  `json:"state"`
	View           string                  `json:"view,omitempty"`
	TypedView      *app.View               `json:"typed_view,omitempty"`
	AllowedIntents []string                `json:"allowed_intents,omitempty"`
	// Intents is the enriched menu the browser renders: one entry per allowed
	// intent, in AllowedIntents order, carrying the intent's title and the
	// single free-text slot (if any) the UI binds its input box to. It is the
	// structured companion to AllowedIntents (which stays for back-compat).
	Intents       []intentInfo            `json:"intents,omitempty"`
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
	}
	return tr
}
