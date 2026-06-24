package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/expr"
	"kitsoki/internal/host"
	"kitsoki/internal/render"
)

// This file owns the operator SURFACE for the ambient miner (slice 4 of the
// ad-hoc-workbench epic). It is deliberately thin: the miner service, the
// proposer, and the apply-via-reload path live in the runtime sibling slices
// (ambient-session-miner / mining-proposal-loop / agent-write-mode-opt-in).
// What lives here:
//
//   - MineProposal / MineState — the injectable queue + miner status the
//     surface reads. The runtime miner pushes into this; the surface never
//     produces proposals itself.
//   - MinerService — the DI seam the /mine control verbs drive (pause/resume/
//     scope/now). A nil service degrades every control verb to a polite hint,
//     so the surface compiles and renders before the runtime lands.
//   - MineCommand — the `/mine` ChatBlockCommand (status|pause|resume|now|
//     scope|queue|accept|refine|dismiss), mirroring ProviderCommand/ModelCommand.
//   - proposalsBadge — the footer chip (`proposals: N`), rendered through a
//     pongo2 template exactly like ideFooterChip, hide-when-zero.
//
// The proposal CARD is NOT new: a queued proposal is an OperatorQuestion-shaped
// payload (accept|refine|dismiss) rendered by the existing operator-question
// widget. ProposalAsOperatorQuestion is the one adapter.

// MineProposalKind distinguishes the two note kinds the inbox carries. They
// look identical on the desk (one gesture); the kind only drives the card
// header and the attention variant.
type MineProposalKind string

const (
	// MineKindStructure is a "capture this as intent/room/binding/gate?"
	// proposal from the mining loop. Non-blocking: no agent is parked.
	MineKindStructure MineProposalKind = "structure"
	// MineKindWriteMode is a "may I make this edit?" opt-in. The agent is
	// parked mid-turn waiting on the verdict, so it drives the attention
	// (orange) badge variant.
	MineKindWriteMode MineProposalKind = "write_mode"
)

// MineProposal is one queued sticky note. The surface renders it; the recorded
// verdict (accept/refine/dismiss) is emitted by the runtime sibling, not here.
type MineProposal struct {
	ID     string
	Kind   MineProposalKind
	Title  string // short headline, e.g. "Capture `make render` after every doc edit"
	Detail string // optional body, e.g. the draft snippet
	Target string // what it touches (a state path, a file, …) — shown in `/mine queue`
}

// MineState is the injectable miner status + proposal queue the surface reads.
// It is a value snapshot the runtime miner refreshes; the surface never mutates
// it except through MinerService (which the runtime owns).
type MineState struct {
	// Enabled mirrors the session's mining.enabled flag. The badge only shows
	// when the miner is enabled AND the queue is non-empty (the proposal
	// proposes nothing until consent is accepted).
	Enabled bool
	// MinedThrough is the watermark — the timestamp of the last transcript
	// turn folded into the miner. Zero means "nothing mined yet".
	MinedThrough time.Time
	// LastRun is when the miner last completed a pass. Zero means "never run".
	LastRun time.Time
	// TranscriptDirs is the set of transcript directories feeding the miner.
	TranscriptDirs []string
	// Queue is the FIFO of pending proposals (structure + write-mode), oldest
	// first, mirroring the web store's ordering.
	Queue []MineProposal
}

// attentionPending reports whether any queued proposal is a parked write-mode
// opt-in — the condition that flips the web badge to its orange variant.
func (s MineState) attentionPending() bool {
	for _, p := range s.Queue {
		if p.Kind == MineKindWriteMode {
			return true
		}
	}
	return false
}

// MinerService is the DI seam the `/mine` control verbs drive. The runtime
// ambient-session-miner slice provides the production implementation; a nil
// service (the default until that slice lands, and in surface-only tests)
// degrades every verb to a polite "miner not wired" hint rather than panicking.
//
// Pause/Resume are synchronous (they flip mining.enabled and return the new
// state). Now kicks an async pass and is expected to be cheap to call (the
// pass itself runs on the background-jobs runner); the surface treats it as
// fire-and-forget and reports completion through minePassDoneMsg.
type MinerService interface {
	// State returns the current miner status + queue snapshot.
	State() MineState
	// Pause sets mining.enabled=false for the session; returns the new state.
	Pause() MineState
	// Resume sets mining.enabled=true; returns the new state.
	Resume() MineState
	// SetScope adds (toggles off when already present) a transcript dir and
	// returns the resulting set.
	SetScope(dir string) []string
	// RunNow forces a mining pass. It runs synchronously off the UI thread
	// (the caller wraps it in a tea.Cmd) and returns the post-pass state.
	RunNow() (MineState, error)
	// Decide records the operator's verdict for a queued proposal by id.
	// verdict is one of "accept" | "refine" | "dismiss". It returns the
	// post-decision state (the proposal is removed from the queue on accept
	// or dismiss).
	Decide(id, verdict string) (MineState, error)
}

// WithMinerService injects the ambient miner's control seam. Omitted (or nil)
// leaves the surface read-only against whatever MineState was last pushed, and
// every control verb returns a "miner not wired" hint.
func WithMinerService(svc MinerService) RootModelOption {
	return func(m *RootModel) { m.minerService = svc }
}

// WithMineState seeds a MineState snapshot directly. Production refreshes this
// from the miner service; tests use it to drive the badge and queue rendering
// without a full service.
func WithMineState(s MineState) RootModelOption {
	return func(m *RootModel) { m.mineStateValue = s }
}

// mineState resolves the live state: the injected service's snapshot when a
// service is wired, else the last-pushed MineState value.
func (m RootModel) mineState() MineState {
	if m.minerService != nil {
		return m.minerService.State()
	}
	return m.mineStateValue
}

// ─── badge ──────────────────────────────────────────────────────────────────

// proposalsBadgeTemplate is the pongo2 source for the footer proposals chip.
// Rendered only when the queue is non-empty so it hides (and drops its `·`
// separator) at zero — the same contract ideFooterChip and inboxBadge honour.
// No hand-rolled fmt.Sprintf builds this operator-visible string.
const proposalsBadgeTemplate = `{% if args.proposals.count > 0 %}proposals: {{ args.proposals.count }}{% endif %}`

// proposalsBadge renders the footer proposals chip through render.Pongo against
// the live queue depth, copying ideFooterChip's structure. Returns "" (hidden)
// when the miner is disabled or the queue is empty. The badge is pure View()
// state: it never changes m.mode and never emits a turn-interrupting tea.Cmd.
func (m RootModel) proposalsBadge() string {
	s := m.mineState()
	count := 0
	if s.Enabled {
		count = len(s.Queue)
	}
	env := expr.Env{
		Slots: map[string]any{},
		World: map[string]any{},
		Event: map[string]any{},
		Args: map[string]any{
			"proposals": map[string]any{
				"count": count,
			},
		},
	}
	out, err := render.Pongo(proposalsBadgeTemplate, env)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ─── card adapter ─────────────────────────────────────────────────────────────

// ProposalAsOperatorQuestion adapts a queued proposal into the OperatorQuestion
// shape the existing operator-question widget renders, so the proposal card is
// the SAME surface as an agent question or a checkpoint decision. The three
// options are the one gesture the operator learns: accept / refine / dismiss.
func ProposalAsOperatorQuestion(p MineProposal) host.OperatorQuestion {
	header := "Capture as structure?"
	if p.Kind == MineKindWriteMode {
		header = "May I edit?"
	}
	question := p.Title
	if p.Detail != "" {
		question += "\n" + p.Detail
	}
	return host.OperatorQuestion{
		Question: question,
		Header:   header,
		Options: []host.OperatorOption{
			{Label: "accept"},
			{Label: "refine"},
			{Label: "dismiss"},
		},
	}
}

// ─── command ──────────────────────────────────────────────────────────────────

// MineCommand implements `/mine`, the operator intercom to the ambient miner.
// It mirrors ProviderCommand/ModelCommand: a ChatBlockCommand that reads the
// injected miner state and drives the MinerService. Sub-verbs:
//
//	/mine            status + the sub-verb help
//	/mine status     watermark, queue depth, last-run time (read-only)
//	/mine pause      pause the ambient miner (mining.enabled=false)
//	/mine resume     resume it
//	/mine now        force a pass (async tea.Cmd; non-blocking)
//	/mine scope <d>  add/remove a transcript dir; echoes the resulting set
//	/mine queue      list pending proposals (id, kind, target) — one list
//	/mine accept <id> accept by id (headless apply, same recorded verdict)
//	/mine refine <id> request refinement by id (recorded refinement signal)
//	/mine dismiss <id> dismiss by id (recorded negative signal)
//
// accept/refine/dismiss by id are CLI aliases for the card gesture — same code
// path — so a scripted flow can drive the loop without the modal.
type MineCommand struct{}

func (MineCommand) Name() string { return "/mine" }

func (MineCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	if len(args) == 0 {
		return mineStatusBlock(m) + "\n\n" + mineHelpBlock(m), m, nil
	}
	switch strings.ToLower(args[0]) {
	case "status":
		return mineStatusBlock(m), m, nil

	case "pause":
		if m.minerService == nil {
			return blockSlashLine(m, "(mine: the miner is not wired in this session — nothing to pause)"), m, nil
		}
		s := m.minerService.Pause()
		m.mineStateValue = s
		return blockSlashLine(m, "(mine: paused — ambient mining is off for this session)"), m, nil

	case "resume":
		if m.minerService == nil {
			return blockSlashLine(m, "(mine: the miner is not wired in this session — nothing to resume)"), m, nil
		}
		s := m.minerService.Resume()
		m.mineStateValue = s
		return blockSlashLine(m, "(mine: resumed — ambient mining is on)"), m, nil

	case "now":
		if m.minerService == nil {
			return blockSlashLine(m, "(mine: the miner is not wired in this session — cannot force a pass)"), m, nil
		}
		return blockSlashLine(m, "(mine: forcing a pass…)"), m, minePassNowCmd(m.minerService)

	case "scope":
		if len(args) < 2 {
			return blockSlashLine(m, "(mine: usage — /mine scope <transcript-dir>)"), m, nil
		}
		if m.minerService == nil {
			return blockSlashLine(m, "(mine: the miner is not wired in this session — cannot change scope)"), m, nil
		}
		dirs := m.minerService.SetScope(args[1])
		s := m.minerService.State()
		m.mineStateValue = s
		return blockSlashLine(m, fmt.Sprintf("(mine: transcript dirs → %s)", scopeSummary(dirs))), m, nil

	case "queue":
		return mineQueueBlock(m), m, nil

	case "accept", "refine", "dismiss":
		verdict := strings.ToLower(args[0])
		if len(args) < 2 {
			return blockSlashLine(m, fmt.Sprintf("(mine: usage — /mine %s <proposal-id>; run /mine queue to list ids)", verdict)), m, nil
		}
		if m.minerService == nil {
			return blockSlashLine(m, "(mine: the miner is not wired in this session — cannot decide a proposal)"), m, nil
		}
		id := args[1]
		s, err := m.minerService.Decide(id, verdict)
		if err != nil {
			return blockSlashLine(m, fmt.Sprintf("(mine: %v)", err)), m, nil
		}
		m.mineStateValue = s
		return blockSlashLine(m, fmt.Sprintf("(mine: %s proposal %s)", verdictPastTense(verdict), id)), m, nil

	default:
		return blockSlashLine(m, fmt.Sprintf("(mine: unknown sub-command %q — try status, pause, resume, now, scope, queue, accept, refine, dismiss)", args[0])), m, nil
	}
}

// mineStatusBlock renders the read-only status line: enabled flag, watermark,
// last-run time, queue depth, and the transcript scope.
func mineStatusBlock(m RootModel) string {
	s := m.mineState()
	state := "paused"
	if s.Enabled {
		state = "active"
	}
	lines := []string{
		fmt.Sprintf("miner: %s", state),
		fmt.Sprintf("mined through: %s", relTime(s.MinedThrough)),
		fmt.Sprintf("last run: %s", relTime(s.LastRun)),
		fmt.Sprintf("queue: %d pending", len(s.Queue)),
		fmt.Sprintf("scope: %s", scopeSummary(s.TranscriptDirs)),
	}
	return blockSlashLine(m, strings.Join(lines, "\n"))
}

// mineHelpBlock lists the sub-verbs, mirroring a bare /provider listing.
func mineHelpBlock(m RootModel) string {
	lines := []string{
		"/mine status        — watermark, queue depth, last-run time",
		"/mine pause         — pause ambient mining for this session",
		"/mine resume        — resume it",
		"/mine now           — force a pass now (non-blocking)",
		"/mine scope <dir>   — add/remove a transcript dir",
		"/mine queue         — list pending proposals",
		"/mine accept <id>   — accept a proposal by id",
		"/mine refine <id>   — ask for a refined proposal by id",
		"/mine dismiss <id>  — dismiss a proposal by id",
	}
	return blockSlashLine(m, strings.Join(lines, "\n"))
}

// mineQueueBlock lists pending proposals — structure and write-mode in one list
// — as id · kind · target, oldest first. Empty queue renders a friendly note.
func mineQueueBlock(m RootModel) string {
	s := m.mineState()
	if len(s.Queue) == 0 {
		return blockSlashLine(m, "(mine: no pending proposals)")
	}
	lines := make([]string, 0, len(s.Queue))
	for _, p := range s.Queue {
		target := p.Target
		if target == "" {
			target = p.Title
		}
		lines = append(lines, fmt.Sprintf("%s · %s · %s", p.ID, p.Kind, target))
	}
	return blockSlashLine(m, strings.Join(lines, "\n"))
}

// scopeSummary renders the transcript dir set for a status/scope echo.
func scopeSummary(dirs []string) string {
	if len(dirs) == 0 {
		return "(default project transcripts)"
	}
	sorted := append([]string(nil), dirs...)
	sort.Strings(sorted)
	return strings.Join(sorted, ", ")
}

// relTime renders a watermark/last-run timestamp as a short relative phrase,
// reusing the inbox humaniser so the surface reads consistently.
func relTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	if d < 0 {
		d = 0
	}
	return humanizeDuration(d) + " ago"
}

// verdictPastTense renders accept/dismiss as a past-tense echo word.
func verdictPastTense(verdict string) string {
	switch verdict {
	case "accept":
		return "accepted"
	case "refine":
		return "refined"
	case "dismiss":
		return "dismissed"
	default:
		return verdict
	}
}

// ─── async messages ───────────────────────────────────────────────────────────

// minePassDoneMsg reports the completion of a `/mine now` forced pass. The
// surface refreshes its state snapshot from the post-pass result.
type minePassDoneMsg struct {
	state MineState
	err   error
}

// minePassNowCmd wraps a forced pass as a tea.Cmd so it runs off the UI thread
// (the pass itself is on the background-jobs runner). Non-blocking: the command
// returns a minePassDoneMsg the model folds back into its state.
func minePassNowCmd(svc MinerService) tea.Cmd {
	return func() tea.Msg {
		s, err := svc.RunNow()
		return minePassDoneMsg{state: s, err: err}
	}
}

// handleMinePassDone folds a forced-pass completion back into the model and
// echoes the outcome. Pure state + transcript; never changes mode.
func (m RootModel) handleMinePassDone(msg minePassDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.transcript.AppendSystem(fmt.Sprintf("(mine: pass failed: %v)", msg.err))
		return m, nil
	}
	m.mineStateValue = msg.state
	m.transcript.AppendSystem(fmt.Sprintf("(mine: pass done — %d pending)", len(msg.state.Queue)))
	return m, nil
}
