package tui

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

// optionLabels extracts the option labels from an adapted proposal card.
func optionLabels(q host.OperatorQuestion) []string {
	labels := make([]string, 0, len(q.Options))
	for _, o := range q.Options {
		labels = append(labels, o.Label)
	}
	return labels
}

// fakeMiner is a surface-only MinerService stub: it records control verbs and
// returns a scripted MineState, never touching a real miner / LLM. Mirrors the
// fake-controller pattern the meta-mode tests use.
type fakeMiner struct {
	state   MineState
	scope   []string
	runErr  error
	decided map[string]string // id → verdict
	ranNow  bool
	paused  bool
	resumed bool
}

func newFakeMiner(s MineState) *fakeMiner {
	return &fakeMiner{state: s, scope: append([]string(nil), s.TranscriptDirs...), decided: map[string]string{}}
}

func (f *fakeMiner) State() MineState { return f.state }

func (f *fakeMiner) Pause() MineState {
	f.paused = true
	f.state.Enabled = false
	return f.state
}

func (f *fakeMiner) Resume() MineState {
	f.resumed = true
	f.state.Enabled = true
	return f.state
}

func (f *fakeMiner) SetScope(dir string) []string {
	// Toggle semantics, matching the production contract the surface echoes.
	for i, d := range f.scope {
		if d == dir {
			f.scope = append(f.scope[:i], f.scope[i+1:]...)
			f.state.TranscriptDirs = f.scope
			return f.scope
		}
	}
	f.scope = append(f.scope, dir)
	f.state.TranscriptDirs = f.scope
	return f.scope
}

func (f *fakeMiner) RunNow() (MineState, error) {
	f.ranNow = true
	if f.runErr != nil {
		return f.state, f.runErr
	}
	return f.state, nil
}

func (f *fakeMiner) Decide(id, verdict string) (MineState, error) {
	f.decided[id] = verdict
	// Drop the decided proposal on accept/dismiss.
	if verdict == "accept" || verdict == "dismiss" {
		kept := f.state.Queue[:0:0]
		for _, p := range f.state.Queue {
			if p.ID != id {
				kept = append(kept, p)
			}
		}
		f.state.Queue = kept
	}
	return f.state, nil
}

func mineTestModel(svc MinerService) RootModel {
	m := RootModel{minerService: svc}
	m.transcript = newTranscriptModel(80, 24)
	return m
}

func sampleQueue() []MineProposal {
	return []MineProposal{
		{ID: "p1", Kind: MineKindStructure, Title: "Capture `make render` gate", Target: "states.docs"},
		{ID: "p2", Kind: MineKindStructure, Title: "Add a lint binding", Target: "states.code"},
		{ID: "p3", Kind: MineKindWriteMode, Title: "May I edit README.md?", Target: "README.md"},
	}
}

// TestMineCommand_StatusPauseResume drives the read-only and pause/resume verbs
// through MineCommand.Run, asserting the rendered block and the mutated miner
// state. No LLM — the miner is a stub.
func TestMineCommand_StatusPauseResume(t *testing.T) {
	t.Parallel()
	f := newFakeMiner(MineState{Enabled: true, Queue: sampleQueue(), TranscriptDirs: []string{"~/.claude/projects/foo"}})
	m := mineTestModel(f)

	// /mine status — read-only, lists the watermark/queue/scope.
	body, _, cmd := MineCommand{}.Run(m, []string{"status"})
	require.Nil(t, cmd)
	for _, want := range []string{"miner: active", "queue: 3 pending", "scope:", "foo"} {
		assert.Containsf(t, body, want, "status block missing %q\n%s", want, body)
	}

	// /mine pause — flips enabled and records it on the service.
	body, _, _ = MineCommand{}.Run(m, []string{"pause"})
	assert.Contains(t, body, "paused")
	assert.True(t, f.paused, "pause should drive the service")

	// /mine resume — symmetric.
	body, _, _ = MineCommand{}.Run(m, []string{"resume"})
	assert.Contains(t, body, "resumed")
	assert.True(t, f.resumed, "resume should drive the service")
}

// TestMineCommand_NowDispatchesAsyncCmd asserts /mine now returns a tea.Cmd (it
// does NOT block) and that running the cmd drives the service and yields a
// minePassDoneMsg the model folds back.
func TestMineCommand_NowDispatchesAsyncCmd(t *testing.T) {
	t.Parallel()
	f := newFakeMiner(MineState{Enabled: true})
	m := mineTestModel(f)

	body, _, cmd := MineCommand{}.Run(m, []string{"now"})
	assert.Contains(t, body, "forcing a pass")
	require.NotNil(t, cmd, "/mine now must return an async cmd, not block")

	msg := cmd()
	done, ok := msg.(minePassDoneMsg)
	require.True(t, ok, "cmd should yield a minePassDoneMsg, got %T", msg)
	assert.NoError(t, done.err)
	assert.True(t, f.ranNow, "running the cmd should drive RunNow")
}

// TestMineCommand_NowSurfacesError checks a failed pass echoes through
// handleMinePassDone without crashing.
func TestMineCommand_NowSurfacesError(t *testing.T) {
	t.Parallel()
	f := newFakeMiner(MineState{Enabled: true})
	f.runErr = errors.New("boom")
	m := mineTestModel(f)

	_, _, cmd := MineCommand{}.Run(m, []string{"now"})
	require.NotNil(t, cmd)
	updated, _ := m.handleMinePassDone(cmd().(minePassDoneMsg))
	rm := updated.(RootModel)
	assert.Contains(t, GetTranscriptContent(rm), "pass failed")
}

// TestMineCommand_Scope adds a dir and echoes the resulting set.
func TestMineCommand_Scope(t *testing.T) {
	t.Parallel()
	f := newFakeMiner(MineState{Enabled: true})
	m := mineTestModel(f)

	body, _, _ := MineCommand{}.Run(m, []string{"scope", "~/.claude/projects/bar"})
	assert.Contains(t, body, "bar")
	assert.Equal(t, []string{"~/.claude/projects/bar"}, f.scope)

	// Bare /mine scope shows usage.
	body, _, _ = MineCommand{}.Run(m, []string{"scope"})
	assert.Contains(t, body, "usage")
}

// TestMineCommand_QueueListsBothKinds asserts structure + write-mode items land
// in one list with id · kind · target.
func TestMineCommand_QueueListsBothKinds(t *testing.T) {
	t.Parallel()
	f := newFakeMiner(MineState{Enabled: true, Queue: sampleQueue()})
	m := mineTestModel(f)

	body, _, _ := MineCommand{}.Run(m, []string{"queue"})
	for _, want := range []string{"p1", "p2", "p3", string(MineKindStructure), string(MineKindWriteMode), "README.md"} {
		assert.Containsf(t, body, want, "queue block missing %q\n%s", want, body)
	}

	// Empty queue renders a friendly note.
	body, _, _ = MineCommand{}.Run(mineTestModel(newFakeMiner(MineState{Enabled: true})), []string{"queue"})
	assert.Contains(t, body, "no pending")
}

// TestMineCommand_DecideById drives the CLI alias for the card gesture — the
// same recorded verdict path a scripted flow uses without the modal.
func TestMineCommand_DecideById(t *testing.T) {
	t.Parallel()
	f := newFakeMiner(MineState{Enabled: true, Queue: sampleQueue()})
	m := mineTestModel(f)

	body, _, _ := MineCommand{}.Run(m, []string{"accept", "p1"})
	assert.Contains(t, body, "accepted proposal p1")
	assert.Equal(t, "accept", f.decided["p1"])

	body, _, _ = MineCommand{}.Run(m, []string{"refine", "p2"})
	assert.Contains(t, body, "refined proposal p2")
	assert.Equal(t, "refine", f.decided["p2"])

	body, _, _ = MineCommand{}.Run(m, []string{"dismiss", "p3"})
	assert.Contains(t, body, "dismissed proposal p3")
	assert.Equal(t, "dismiss", f.decided["p3"])

	// Missing id shows usage.
	body, _, _ = MineCommand{}.Run(m, []string{"accept"})
	assert.Contains(t, body, "usage")
}

// TestMineCommand_NilServiceDegrades confirms every control verb degrades to a
// polite hint (never panics) when no miner is wired — the default posture until
// the runtime sibling lands.
func TestMineCommand_NilServiceDegrades(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)

	for _, verb := range [][]string{{"pause"}, {"resume"}, {"now"}, {"scope", "x"}, {"accept", "p1"}, {"refine", "p1"}, {"dismiss", "p1"}} {
		body, _, cmd := MineCommand{}.Run(m, verb)
		assert.Containsf(t, body, "not wired", "%v should degrade with a hint\n%s", verb, body)
		assert.Nilf(t, cmd, "%v should not dispatch a cmd without a service", verb)
	}

	// Read-only verbs still render against the empty snapshot.
	body, _, _ := MineCommand{}.Run(m, []string{"status"})
	assert.Contains(t, body, "miner: paused")
}

// TestMineCommand_UnknownSubVerb rejects garbage with the verb list.
func TestMineCommand_UnknownSubVerb(t *testing.T) {
	t.Parallel()
	m := mineTestModel(newFakeMiner(MineState{Enabled: true}))
	body, _, _ := MineCommand{}.Run(m, []string{"frobnicate"})
	assert.Contains(t, body, "unknown sub-command")
}

// TestProposalsBadge_Render feeds a 3-item enabled queue, asserts the badge
// renders `proposals: 3` and sits on the framework footer row; an empty/
// disabled queue renders no badge (the hide-when-zero contract). Mirrors the
// footer regression suite's framework-line assertion.
func TestProposalsBadge_Render(t *testing.T) {
	t.Parallel()

	withQueue := RootModel{}
	withQueue.transcript = newTranscriptModel(80, 24)
	withQueue.location = newLocationModel()
	withQueue.mineStateValue = MineState{Enabled: true, Queue: sampleQueue()}
	assert.Equal(t, "proposals: 3", withQueue.proposalsBadge())
	assert.Contains(t, FooterLine1ForTest(withQueue), "proposals: 3",
		"the badge must sit on the framework footer row")

	// Empty queue ⇒ no badge ⇒ no segment.
	empty := RootModel{}
	empty.transcript = newTranscriptModel(80, 24)
	empty.location = newLocationModel()
	empty.mineStateValue = MineState{Enabled: true}
	assert.Equal(t, "", empty.proposalsBadge(), "empty queue must hide the badge")
	assert.NotContains(t, FooterLine1ForTest(empty), "proposals:")

	// Disabled miner with a queue ⇒ still hidden (consent not accepted).
	disabled := RootModel{}
	disabled.transcript = newTranscriptModel(80, 24)
	disabled.location = newLocationModel()
	disabled.mineStateValue = MineState{Enabled: false, Queue: sampleQueue()}
	assert.Equal(t, "", disabled.proposalsBadge(), "disabled miner must hide the badge")
}

// TestProposalAsOperatorQuestion confirms a queued proposal adapts to the
// operator-question card with the one gesture (accept/refine/dismiss) and a
// kind-appropriate header — so the card is the reused surface, not a new one.
func TestProposalAsOperatorQuestion(t *testing.T) {
	t.Parallel()

	structure := ProposalAsOperatorQuestion(MineProposal{Kind: MineKindStructure, Title: "Capture gate", Detail: "make render"})
	assert.Equal(t, "Capture as structure?", structure.Header)
	assert.Contains(t, structure.Question, "Capture gate")
	assert.Contains(t, structure.Question, "make render")
	labels := optionLabels(structure)
	assert.Equal(t, []string{"accept", "refine", "dismiss"}, labels)

	write := ProposalAsOperatorQuestion(MineProposal{Kind: MineKindWriteMode, Title: "May I edit X?"})
	assert.Equal(t, "May I edit?", write.Header)
}

// TestMineState_AttentionPending flags write-mode opt-ins for the orange badge.
func TestMineState_AttentionPending(t *testing.T) {
	t.Parallel()
	assert.False(t, MineState{Queue: []MineProposal{{Kind: MineKindStructure}}}.attentionPending())
	assert.True(t, MineState{Queue: []MineProposal{{Kind: MineKindWriteMode}}}.attentionPending())
}
