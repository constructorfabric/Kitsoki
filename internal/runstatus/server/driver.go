package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/render/elements"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
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

	// RewindRoute reverses one contextual-routing (CRR) decision, identified by
	// its stable decisionID ("<session_id>:<turn_number>"), and re-dispatches the
	// original utterance under newClass. It backs the runstatus.session.rewind_route
	// RPC the web route-receipt chip's "rewind" affordance calls. newClass may be
	// empty for the lane classes the engine reverses today (help / room_request /
	// meta_edit), in which case the engine reuses the journaled class; an
	// intent-class rewind is not yet recoverable from the journal and returns an
	// explicit error the surface presents gracefully (a disabled control), not a 500.
	RewindRoute(ctx context.Context, decisionID string, newClass orchestrator.ContextRouteClass, reason string) (*orchestrator.TurnOutcome, error)

	// RecordRoutingFeedback journals an operator's up/down verdict on a
	// previously-routed turn (WS-C C4: the routing-dissatisfaction substrate).
	// It backs the runstatus.session.routing_feedback RPC the web chat's
	// thumbs-up/down control calls — the browser-side twin of the TUI's
	// `/route up|down` command. state/intent/phrase/tier are recovered by the
	// browser from the turn's own trace events (see readTurnRouting in
	// stores/run.ts), exactly like the TUI recovers them from its routing
	// pipeline snapshot; this method does no lookup of its own. It never
	// mutates session/world state (a standalone journal write), so it is safe
	// to call without the session writer lock.
	RecordRoutingFeedback(ctx context.Context, statePath, intent, phrase, tier string, verdict orchestrator.RoutingFeedbackVerdict) error
}

// OperationDriver is an optional live-session extension for driving an
// operation handle until it reaches a terminal/waiting checkpoint or there is no
// safe autonomous next action. Kept off the core Driver interface so read-only
// and white-box test drivers do not need to implement operation machinery.
type OperationDriver interface {
	DriveOperation(ctx context.Context) (*orchestrator.OperationDriveOutcome, error)
}

// BackgroundOperationDriver is the automatic twin of OperationDriver. It only
// advances a running operation when the active handle declares
// run_in_background:true; explicit DriveOperation remains available for
// operator-requested foreground/supervised drives.
type BackgroundOperationDriver interface {
	DriveBackgroundOperation(ctx context.Context) (*orchestrator.OperationDriveOutcome, error)
}

// WorkLister is an optional read-only extension for Drivers that can expose the
// session's current async work queue. The web server type-asserts it for the
// runstatus.work.list RPC; read-only or test drivers without a JobStore can
// omit it and simply contribute no work rows.
type WorkLister interface {
	ListWork(ctx context.Context) (SessionWork, error)
}

// ChatShower is an optional read-only extension for Drivers that can expose a
// focused chat transcript for async reacquisition. It backs runstatus.chat.show,
// the web equivalent of the studio MCP chat.show tool.
type ChatShower interface {
	ShowChat(ctx context.Context, chatID string, sinceSeq int) (ChatShowResult, error)
}

// GitHubInboxSyncer is an optional live-session extension for importing
// GitHub issue/PR work into the session inbox. Read-only trace surfaces omit it.
type GitHubInboxSyncer interface {
	SyncGitHubInbox(ctx context.Context, opts GitHubInboxSyncOptions) (GitHubInboxSyncResult, error)
}

// GitHubInboxSyncOptions controls GitHub issue/PR inbox discovery for one
// runstatus session.
type GitHubInboxSyncOptions struct {
	Repo            string
	IncludeIssues   bool
	IncludePRs      bool
	Assignee        string
	ReviewRequested string
	Limit           int
	TeleportState   string
}

// GitHubInboxSyncResult is the JSON-RPC result from
// runstatus.session.inbox.sync_github.
type GitHubInboxSyncResult struct {
	OK        bool                        `json:"ok"`
	SessionID string                      `json:"session_id"`
	Fetched   int                         `json:"fetched"`
	Inserted  int                         `json:"inserted"`
	Skipped   int                         `json:"skipped"`
	Items     []GitHubInboxSyncResultItem `json:"items"`
}

// GitHubInboxSyncResultItem is one imported or skipped GitHub row.
type GitHubInboxSyncResultItem struct {
	NotificationID string         `json:"notification_id"`
	Kind           string         `json:"kind"`
	Number         string         `json:"number"`
	Title          string         `json:"title"`
	URL            string         `json:"url,omitempty"`
	Inserted       bool           `json:"inserted"`
	OriginRef      string         `json:"origin_ref"`
	TeleportState  string         `json:"teleport_state"`
	TeleportSlots  map[string]any `json:"teleport_slots,omitempty"`
}

// SessionWork is the per-session async queue returned by [WorkLister].
type SessionWork struct {
	Summary WorkSummary `json:"summary"`
	Items   []WorkItem  `json:"items,omitempty"`
}

// WorkSummary gives the browser a cheap global signal before it renders the
// detailed rows.
type WorkSummary struct {
	Items                       int `json:"items"`
	NeedsAttention              int `json:"needs_attention"`
	JobsRunning                 int `json:"jobs_running"`
	JobsAwaitingInput           int `json:"jobs_awaiting_input"`
	JobsTerminal                int `json:"jobs_terminal"`
	NotificationsUnread         int `json:"notifications_unread"`
	NotificationsActionRequired int `json:"notifications_action_required"`
	PendingDrives               int `json:"pending_drives"`
	DispatchingDrives           int `json:"dispatching_drives"`
	FailedDrives                int `json:"failed_drives"`
	BackgroundedChats           int `json:"backgrounded_chats"`
	OperatorQuestions           int `json:"operator_questions"`
	MiningProposals             int `json:"mining_proposals"`
}

// WorkItem is one active row in the operator's work queue. Notification rows
// can teleport, job rows jump through their matching notification when one is
// available or reacquire the owning session otherwise, and chat-backed rows
// reacquire focused context through runstatus.chat.show.
type WorkItem struct {
	Kind               string                           `json:"kind"`
	Priority           int                              `json:"priority"`
	SessionID          string                           `json:"session_id"`
	Title              string                           `json:"title,omitempty"`
	Body               string                           `json:"body,omitempty"`
	Status             string                           `json:"status,omitempty"`
	NotificationID     string                           `json:"notification_id,omitempty"`
	JobID              string                           `json:"job_id,omitempty"`
	Severity           jobs.NotificationSeverity        `json:"severity,omitempty"`
	CreatedAt          time.Time                        `json:"created_at,omitempty"`
	UpdatedAt          time.Time                        `json:"updated_at,omitempty"`
	ReadAt             *time.Time                       `json:"read_at,omitempty"`
	TeleportState      string                           `json:"teleport_state,omitempty"`
	TeleportSlots      map[string]any                   `json:"teleport_slots,omitempty"`
	TeleportJobID      string                           `json:"teleport_job_id,omitempty"`
	OriginKind         string                           `json:"origin_kind,omitempty"`
	OriginRef          string                           `json:"origin_ref,omitempty"`
	OriginURL          string                           `json:"origin_url,omitempty"`
	OriginState        string                           `json:"origin_state,omitempty"`
	ReacquireTool      string                           `json:"reacquire_tool"`
	ReacquireSessionID string                           `json:"reacquire_session_id,omitempty"`
	DriveID            string                           `json:"drive_id,omitempty"`
	ChatID             string                           `json:"chat_id,omitempty"`
	QuestionID         string                           `json:"question_id,omitempty"`
	ProposalID         string                           `json:"proposal_id,omitempty"`
	ProposalKind       string                           `json:"proposal_kind,omitempty"`
	ProposalTarget     string                           `json:"proposal_target,omitempty"`
	DraftPath          string                           `json:"draft_path,omitempty"`
	Rung               int                              `json:"rung,omitempty"`
	Questions          []kitsokimcp.OperatorAskQuestion `json:"questions,omitempty"`
	Actor              string                           `json:"actor,omitempty"`
	Thread             string                           `json:"thread,omitempty"`
	TmuxSession        string                           `json:"tmux_session,omitempty"`
	TmuxHost           string                           `json:"tmux_host,omitempty"`
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
	// Chats is the optional chat store backing pending chat drives and
	// tmux-hosted background chats. Nil means the work queue omits chat work.
	Chats *chats.Store
	// TraceHistory reads the live session trace. It is optional so read-only
	// tests and trace-less surfaces can omit mining proposal work.
	TraceHistory func() store.History
}

// ErrNoInbox is returned by Teleport when the session has no JobStore wired, so
// the surface can distinguish "no inbox here" from a genuine teleport failure.
var ErrNoInbox = errors.New("session has no inbox configured")

// ErrNotTeleportable is returned by Teleport when the notification exists but
// carries no destination state (an informational item); the surface renders it
// read-only rather than as a dead link.
var ErrNotTeleportable = errors.New("notification is not teleportable")

// turnSupplementsKey carries per-turn supplemental slots from the transport onto
// a free-text Turn. A surface attaches lightweight ambient context (e.g. the deck
// scene the operator is currently viewing) so it merges into the routed intent's
// slots — gap-filling only; the harness's own classification always wins. Carried
// on the ctx (like the visual ambient) so the Driver interface stays unchanged.
type turnSupplementsKey struct{}

// WithTurnSupplements attaches supplemental slots to ctx for the next Turn.
func WithTurnSupplements(ctx context.Context, slots world.Slots) context.Context {
	if len(slots) == 0 {
		return ctx
	}
	return context.WithValue(ctx, turnSupplementsKey{}, slots)
}

func turnSupplementsFromCtx(ctx context.Context) world.Slots {
	s, _ := ctx.Value(turnSupplementsKey{}).(world.Slots)
	return s
}

func (d OrchestratorDriver) Turn(ctx context.Context, input string) (*orchestrator.TurnOutcome, error) {
	if sup := turnSupplementsFromCtx(ctx); len(sup) > 0 {
		return d.Orch.Turn(ctx, d.SID, input, orchestrator.WithSupplementSlots(sup))
	}
	return d.Orch.Turn(ctx, d.SID, input)
}

func (d OrchestratorDriver) SubmitDirect(ctx context.Context, intent string, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	return d.Orch.SubmitDirect(ctx, d.SID, intent, slots)
}

func (d OrchestratorDriver) ContinueTurn(ctx context.Context, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	return d.Orch.ContinueTurn(ctx, d.SID, slots)
}

func (d OrchestratorDriver) DriveOperation(ctx context.Context) (*orchestrator.OperationDriveOutcome, error) {
	return d.Orch.DriveOperation(ctx, d.SID)
}

func (d OrchestratorDriver) DriveBackgroundOperation(ctx context.Context) (*orchestrator.OperationDriveOutcome, error) {
	return d.Orch.DriveBackgroundOperation(ctx, d.SID)
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
	SetHarnessSelection(profile, model, effort string) error
}

func (d OrchestratorDriver) HarnessProfiles() []orchestrator.ProfileInfo {
	return d.Orch.Profiles()
}

func (d OrchestratorDriver) HarnessSelection() orchestrator.ProfileSelection {
	return d.Orch.Selection()
}

func (d OrchestratorDriver) SetHarnessSelection(profile, model, effort string) error {
	return d.Orch.SetSelection(profile, model, effort)
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

// ListWork returns active async work for this session. By default it includes
// unread notifications plus jobs that are still running, awaiting input, or
// failed. Quiet terminal success/cancel rows stay available through the
// per-session trace/inbox, but do not crowd the global work queue.
func (d OrchestratorDriver) ListWork(ctx context.Context) (SessionWork, error) {
	out := SessionWork{}
	if d.Jobs != nil {
		jobRows, err := d.Jobs.ListBySession(ctx, d.SID)
		if err != nil {
			return SessionWork{}, err
		}
		notifs, err := d.Jobs.ListNotifications(ctx, d.SID, 0)
		if err != nil {
			return SessionWork{}, err
		}
		unread, err := d.Jobs.UnreadCount(ctx, d.SID)
		if err != nil {
			return SessionWork{}, err
		}
		jobNotifications := workJobNotifications(notifs)

		for _, j := range jobRows {
			switch j.Status {
			case jobs.JobRunning:
				out.Summary.JobsRunning++
			case jobs.JobAwaitingInput:
				out.Summary.JobsAwaitingInput++
			case jobs.JobDone, jobs.JobFailed, jobs.JobCancelled:
				out.Summary.JobsTerminal++
			}
			if !activeWorkJob(j) {
				continue
			}
			item := WorkItem{
				Kind:               "job",
				Priority:           workJobPriority(j),
				SessionID:          string(d.SID),
				Title:              j.Kind,
				Body:               jobClarificationPrompt(j),
				Status:             string(j.Status),
				JobID:              j.ID,
				CreatedAt:          j.CreatedAt,
				UpdatedAt:          j.UpdatedAt,
				OriginState:        string(j.OriginState),
				ReacquireTool:      "session",
				ReacquireSessionID: string(d.SID),
			}
			if n, ok := jobNotifications[j.ID]; ok {
				if n.Body != "" {
					item.Body = n.Body
				}
				item.NotificationID = n.ID
				item.Severity = n.Severity
				item.TeleportState = n.TeleportState
				item.TeleportSlots = n.TeleportSlots
				item.TeleportJobID = n.TeleportJobID
				item.OriginKind = n.OriginKind
				item.OriginRef = n.OriginRef
				item.OriginURL = n.OriginURL
				item.ReacquireTool = "notification"
			}
			out.Items = append(out.Items, item)
		}
		for _, count := range unread {
			out.Summary.NotificationsUnread += count
		}
		out.Summary.NotificationsActionRequired = unread[jobs.SeverityActionRequired]
		for _, n := range notifs {
			if n.ReadAt != nil {
				continue
			}
			out.Items = append(out.Items, WorkItem{
				Kind:               "notification",
				Priority:           workNotificationPriority(n),
				SessionID:          string(d.SID),
				Title:              n.Title,
				Body:               n.Body,
				Status:             "unread",
				NotificationID:     n.ID,
				Severity:           n.Severity,
				CreatedAt:          n.CreatedAt,
				UpdatedAt:          n.CreatedAt,
				ReadAt:             n.ReadAt,
				TeleportState:      n.TeleportState,
				TeleportSlots:      n.TeleportSlots,
				TeleportJobID:      n.TeleportJobID,
				OriginKind:         n.OriginKind,
				OriginRef:          n.OriginRef,
				OriginURL:          n.OriginURL,
				ReacquireTool:      "notification",
				ReacquireSessionID: string(d.SID),
			})
		}
	}
	out, err := d.listChatWork(ctx, out)
	if err != nil {
		return SessionWork{}, err
	}
	out = d.listMiningProposalWork(out)
	sort.SliceStable(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
	out.Summary.Items = len(out.Items)
	for _, item := range out.Items {
		if workItemNeedsAttention(item) {
			out.Summary.NeedsAttention++
		}
	}
	return out, nil
}

func (d OrchestratorDriver) listMiningProposalWork(out SessionWork) SessionWork {
	if d.TraceHistory == nil {
		return out
	}
	proposals := pendingMiningProposals(d.TraceHistory())
	out.Summary.MiningProposals += len(proposals)
	for _, proposal := range proposals {
		bodyParts := make([]string, 0, 3)
		if proposal.Target != "" {
			bodyParts = append(bodyParts, "target="+proposal.Target)
		}
		if proposal.Rung != 0 {
			bodyParts = append(bodyParts, fmt.Sprintf("rung=%d", proposal.Rung))
		}
		if proposal.DraftPath != "" {
			bodyParts = append(bodyParts, "draft="+proposal.DraftPath)
		}
		title := strings.TrimSpace(fmt.Sprintf("%s proposal", proposal.Kind))
		if title == "proposal" {
			title = "Mining proposal"
		}
		out.Items = append(out.Items, WorkItem{
			Kind:               "mining_proposal",
			Priority:           58,
			SessionID:          string(d.SID),
			Title:              title,
			Body:               strings.Join(bodyParts, "; "),
			Status:             "awaiting_review",
			UpdatedAt:          proposal.RaisedAt,
			ReacquireTool:      "session",
			ReacquireSessionID: string(d.SID),
			ProposalID:         proposal.RecipeID,
			ProposalKind:       proposal.Kind,
			ProposalTarget:     proposal.Target,
			DraftPath:          proposal.DraftPath,
			Rung:               proposal.Rung,
		})
	}
	return out
}

type miningProposalWorkItem struct {
	RecipeID  string
	Kind      string
	Target    string
	Priority  float64
	Rung      int
	DraftPath string
	RaisedAt  time.Time
}

func pendingMiningProposals(history store.History) []miningProposalWorkItem {
	if len(history) == 0 {
		return nil
	}
	byRecipe := make(map[string]miningProposalWorkItem)
	var order []string
	for _, ev := range history {
		switch ev.Kind {
		case store.MiningProposalRaised:
			var payload store.MiningProposalRaisedPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload.RecipeID == "" {
				continue
			}
			if _, exists := byRecipe[payload.RecipeID]; !exists {
				order = append(order, payload.RecipeID)
			}
			byRecipe[payload.RecipeID] = miningProposalWorkItem{
				RecipeID:  payload.RecipeID,
				Kind:      payload.Kind,
				Target:    payload.Target,
				Priority:  payload.Priority,
				Rung:      payload.Rung,
				DraftPath: payload.DraftPath,
				RaisedAt:  ev.Ts,
			}
		case store.MiningProposalDecided:
			var payload store.MiningProposalDecidedPayload
			if err := json.Unmarshal(ev.Payload, &payload); err != nil || payload.RecipeID == "" {
				continue
			}
			delete(byRecipe, payload.RecipeID)
		}
	}
	out := make([]miningProposalWorkItem, 0, len(byRecipe))
	for _, recipeID := range order {
		if item, ok := byRecipe[recipeID]; ok {
			out = append(out, item)
		}
	}
	return out
}

func jobClarificationPrompt(j jobs.Job) string {
	if j.Status != jobs.JobAwaitingInput {
		return ""
	}
	schema := clarificationSchema(j.ClarificationSchema)
	if schema == nil {
		return ""
	}
	return schema.Prompt
}

func clarificationSchema(raw any) *jobs.ClarificationSchema {
	switch v := raw.(type) {
	case nil:
		return nil
	case jobs.ClarificationSchema:
		return &v
	case *jobs.ClarificationSchema:
		return v
	case map[string]any:
		schema := jobs.ClarificationSchema{Fields: map[string]string{}}
		if prompt, ok := v["prompt"].(string); ok {
			schema.Prompt = prompt
		}
		if fields, ok := v["fields"].(map[string]any); ok {
			for name, typ := range fields {
				if text, ok := typ.(string); ok {
					schema.Fields[name] = text
				}
			}
		}
		return &schema
	default:
		return nil
	}
}

func workJobNotifications(notifs []jobs.Notification) map[string]jobs.Notification {
	out := make(map[string]jobs.Notification)
	for _, n := range notifs {
		if n.ReadAt != nil {
			continue
		}
		jobID := n.TeleportJobID
		if jobID == "" && strings.HasPrefix(n.OriginRef, "job:") {
			jobID = strings.TrimPrefix(n.OriginRef, "job:")
		}
		if jobID == "" {
			continue
		}
		if existing, exists := out[jobID]; !exists || jobNotificationRank(n.Severity) > jobNotificationRank(existing.Severity) {
			out[jobID] = n
		}
	}
	return out
}

func jobNotificationRank(severity jobs.NotificationSeverity) int {
	switch severity {
	case jobs.SeverityActionRequired:
		return 4
	case jobs.SeverityError:
		return 3
	case jobs.SeverityWarn:
		return 2
	default:
		return 1
	}
}

// SyncGitHubInbox imports assigned GitHub issues and requested PR reviews into
// this session's inbox using the same idempotent notification contract as the
// CLI and studio MCP tools.
func (d OrchestratorDriver) SyncGitHubInbox(ctx context.Context, opts GitHubInboxSyncOptions) (GitHubInboxSyncResult, error) {
	if d.Jobs == nil {
		return GitHubInboxSyncResult{}, fmt.Errorf("github inbox sync: no job store configured")
	}
	includeIssues := opts.IncludeIssues
	includePRs := opts.IncludePRs
	if !includeIssues && !includePRs {
		includeIssues = true
		includePRs = true
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	teleportState := strings.TrimSpace(opts.TeleportState)
	if teleportState == "" {
		teleportState = "inbox"
	}
	items, err := host.ListGitHubInboxItems(ctx, host.GitHubInboxOptions{
		Repo:            opts.Repo,
		IncludeIssues:   includeIssues,
		IncludePRs:      includePRs,
		Assignee:        opts.Assignee,
		ReviewRequested: opts.ReviewRequested,
		Limit:           limit,
	})
	if err != nil {
		return GitHubInboxSyncResult{}, err
	}
	out := GitHubInboxSyncResult{
		OK:        true,
		SessionID: string(d.SID),
		Fetched:   len(items),
		Items:     make([]GitHubInboxSyncResultItem, 0, len(items)),
	}
	for _, item := range items {
		n := inbox.NewGitHubNotification(d.SID, opts.Repo, teleportState, item)
		inserted, err := d.Jobs.InsertExternalNotificationOnce(ctx, n)
		if err != nil {
			return GitHubInboxSyncResult{}, fmt.Errorf("github inbox sync: insert notification for %s #%s: %w", item.Kind, item.Number, err)
		}
		if inserted {
			out.Inserted++
		} else {
			out.Skipped++
		}
		out.Items = append(out.Items, GitHubInboxSyncResultItem{
			NotificationID: n.ID,
			Kind:           item.Kind,
			Number:         item.Number,
			Title:          item.Title,
			URL:            item.URL,
			Inserted:       inserted,
			OriginRef:      n.OriginRef,
			TeleportState:  n.TeleportState,
			TeleportSlots:  n.TeleportSlots,
		})
	}
	return out, nil
}

func (d OrchestratorDriver) listChatWork(ctx context.Context, out SessionWork) (SessionWork, error) {
	if d.Chats == nil {
		return out, nil
	}
	drives, err := d.Chats.ListDrivesBySession(ctx, string(d.SID),
		[]chats.DriveStatus{chats.DriveStatusPending, chats.DriveStatusDispatching, chats.DriveStatusFailed})
	if err != nil {
		return SessionWork{}, err
	}
	for _, drive := range drives {
		priority := 65
		kind := "pending_drive"
		if drive.Status == chats.DriveStatusDispatching {
			out.Summary.DispatchingDrives++
			priority = 68
		} else if drive.Status == chats.DriveStatusFailed {
			out.Summary.FailedDrives++
			kind = "failed_drive"
			priority = 94
		} else {
			out.Summary.PendingDrives++
		}
		out.Items = append(out.Items, WorkItem{
			Kind:               kind,
			Priority:           priority,
			SessionID:          string(d.SID),
			Title:              drive.Payload,
			Body:               drive.ErrorMessage,
			Status:             string(drive.Status),
			CreatedAt:          drive.ReceivedAt,
			UpdatedAt:          driveUpdatedAt(drive),
			OriginState:        drive.OriginState,
			ReacquireTool:      "chat.show",
			ReacquireSessionID: string(d.SID),
			DriveID:            drive.DriveID,
			ChatID:             drive.ChatID,
			Actor:              drive.Actor,
			Thread:             drive.Thread,
		})
	}
	ptys, err := d.Chats.ListPTYForHost(ctx)
	if err != nil {
		return SessionWork{}, err
	}
	for _, pty := range ptys {
		if pty.Mode != chats.PtyModeBackground {
			continue
		}
		chat, err := d.Chats.Get(ctx, pty.ChatID)
		if err != nil || chat == nil || chat.SessionID != string(d.SID) {
			continue
		}
		out.Summary.BackgroundedChats++
		out.Items = append(out.Items, WorkItem{
			Kind:               "backgrounded_chat",
			Priority:           60,
			SessionID:          string(d.SID),
			Title:              chat.Title,
			Status:             string(pty.Mode),
			CreatedAt:          pty.CreatedAt,
			UpdatedAt:          pty.UpdatedAt,
			ReacquireTool:      "chat.show",
			ReacquireSessionID: string(d.SID),
			ChatID:             pty.ChatID,
			TmuxSession:        pty.TmuxSession,
			TmuxHost:           pty.TmuxHost,
		})
	}
	return out, nil
}

func driveUpdatedAt(drive chats.Drive) time.Time {
	if drive.CompletedAt != nil {
		return *drive.CompletedAt
	}
	if drive.DispatchedAt != nil {
		return *drive.DispatchedAt
	}
	return drive.ReceivedAt
}

func activeWorkJob(j jobs.Job) bool {
	return j.Status == jobs.JobRunning || j.Status == jobs.JobAwaitingInput || j.Status == jobs.JobFailed
}

func workJobPriority(j jobs.Job) int {
	switch j.Status {
	case jobs.JobAwaitingInput:
		return 96
	case jobs.JobFailed:
		return 90
	case jobs.JobRunning:
		return 70
	default:
		return 25
	}
}

func workNotificationPriority(n jobs.Notification) int {
	switch n.Severity {
	case jobs.SeverityActionRequired:
		return 100
	case jobs.SeverityError:
		return 92
	case jobs.SeverityWarn:
		return 88
	case jobs.SeveritySuccess:
		return 50
	default:
		return 45
	}
}

func workItemNeedsAttention(item WorkItem) bool {
	switch item.Kind {
	case "notification":
		return item.ReadAt == nil && item.Severity == jobs.SeverityActionRequired
	case "job":
		return item.Status == string(jobs.JobAwaitingInput) || item.Status == string(jobs.JobFailed)
	case "failed_drive":
		return true
	case "operator_question":
		return true
	default:
		return false
	}
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
// and Agent Room banner use, so the trace is indistinguishable from a TUI
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

// RewindRoute delegates to Orchestrator.RewindRoute, binding the session id.
// The engine reverses the CRR decision at decisionID and re-dispatches the
// original utterance under newClass; class=intent returns a not-yet-implemented
// error (the original intent isn't recoverable from TurnStarted alone).
func (d OrchestratorDriver) RewindRoute(ctx context.Context, decisionID string, newClass orchestrator.ContextRouteClass, reason string) (*orchestrator.TurnOutcome, error) {
	return d.Orch.RewindRoute(ctx, d.SID, decisionID, newClass, reason)
}

// RecordRoutingFeedback delegates to Orchestrator.RecordRoutingFeedback,
// binding the session id. See the [Driver] doc for the surface contract.
func (d OrchestratorDriver) RecordRoutingFeedback(ctx context.Context, statePath, intent, phrase, tier string, verdict orchestrator.RoutingFeedbackVerdict) error {
	return d.Orch.RecordRoutingFeedback(ctx, d.SID, app.StatePath(statePath), intent, phrase, tier, verdict)
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
	// ContextRoute is the contextual-routing receipt for a turn the CRR tier
	// resolved (nil for deterministic/semantic/LLM turns). It carries the
	// matched class/intent, the contextual confidence, and a stable DecisionID
	// so the web surface can show a "routed to … · contextual" receipt chip.
	ContextRoute *contextRouteInfo `json:"context_route,omitempty"`
	// OperationDrive is present only on runstatus.session.drive_operation
	// responses. It preserves the bounded driver result so web surfaces can tell
	// the operator whether the drive completed, parked at a checkpoint, or hit a
	// safety stop without scraping trace events.
	OperationDrive *operationDriveResult `json:"operation_drive,omitempty"`
}

// contextRouteInfo is the wire shape of orchestrator.ContextRouteReceipt — the
// queryable record of one contextual-routing decision, surfaced to the browser
// so an operator can see (and, in a later slice, rewind) the route. The
// DecisionID is "<session_id>:<turn_number>", the stable rewind target.
type contextRouteInfo struct {
	Class        string  `json:"class"`
	Intent       string  `json:"intent,omitempty"`
	Reason       string  `json:"reason,omitempty"`
	Confidence   float64 `json:"confidence"`
	TargetChatID string  `json:"target_chat_id,omitempty"`
	TargetLane   string  `json:"target_lane,omitempty"`
	DecisionID   string  `json:"decision_id"`
}

type operationDriveResult struct {
	Turns      int    `json:"turns"`
	StopReason string `json:"stop_reason,omitempty"`
	LastIntent string `json:"last_intent,omitempty"`
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
	if cr := out.ContextRoute; cr != nil {
		tr.ContextRoute = &contextRouteInfo{
			Class:        cr.Class,
			Intent:       cr.Intent,
			Reason:       cr.Reason,
			Confidence:   cr.Confidence,
			TargetChatID: cr.TargetChatID,
			TargetLane:   cr.TargetLane,
			DecisionID:   cr.DecisionID,
		}
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

func newTurnResultWithOperationDrive(out *orchestrator.TurnOutcome, resolver Driver, drive *orchestrator.OperationDriveOutcome) turnResult {
	tr := newTurnResult(out, resolver)
	if drive == nil {
		return tr
	}
	tr.OperationDrive = &operationDriveResult{
		Turns:      drive.Turns,
		StopReason: strings.TrimSpace(drive.StopReason),
		LastIntent: strings.TrimSpace(drive.LastIntent),
	}
	return tr
}
