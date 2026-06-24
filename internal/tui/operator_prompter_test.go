package tui

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

// promptCaptureModel is a minimal tea.Model that answers the first
// operatorQuestionMsg it sees with a canned reply, then quits. It lets the
// prompter test drive a real program message loop without the full RootModel.
type promptCaptureModel struct {
	reply map[string]any
	got   chan []host.OperatorQuestion
}

func (m promptCaptureModel) Init() tea.Cmd { return nil }

func (m promptCaptureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch q := msg.(type) {
	case operatorQuestionMsg:
		m.got <- q.questions
		q.answerCh <- m.reply
		return m, tea.Quit
	}
	return m, nil
}

func (m promptCaptureModel) View() string { return "" }

func newHeadlessProgram(model tea.Model) *tea.Program {
	return tea.NewProgram(model,
		tea.WithInput(&bytes.Buffer{}),
		tea.WithOutput(&bytes.Buffer{}),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
	)
}

func TestTUIOperatorPrompter_NoProgramReportsNoOperator(t *testing.T) {
	p := NewTUIOperatorPrompter()
	_, err := p.Ask(context.Background(), "sid", []host.OperatorQuestion{{Question: "q"}})
	require.ErrorIs(t, err, errNoOperator)
}

func TestTUIOperatorPrompter_ForwardsQuestionAndReturnsAnswer(t *testing.T) {
	model := promptCaptureModel{
		reply: map[string]any{"Ship?": "Yes"},
		got:   make(chan []host.OperatorQuestion, 1),
	}
	prog := newHeadlessProgram(model)

	p := NewTUIOperatorPrompter()
	p.Attach(prog)

	runDone := make(chan error, 1)
	go func() {
		_, err := prog.Run()
		runDone <- err
	}()
	// Let the message loop spin up so Send doesn't race the goroutine launch.
	time.Sleep(50 * time.Millisecond)

	questions := []host.OperatorQuestion{{
		Question: "Ship?",
		Header:   "Ship",
		Options:  []host.OperatorOption{{Label: "Yes"}, {Label: "No"}},
	}}
	answers, err := p.Ask(context.Background(), "sid", questions)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"Ship?": "Yes"}, answers)

	// The model saw the forwarded question verbatim.
	select {
	case got := <-model.got:
		require.Len(t, got, 1)
		assert.Equal(t, "Ship?", got[0].Question)
	case <-time.After(time.Second):
		t.Fatal("model never received the operatorQuestionMsg")
	}

	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("program did not exit")
	}
}

func TestTUIOperatorPrompter_CtxCancelUnblocks(t *testing.T) {
	// silentModel swallows the question without answering, so Ask must rely on
	// ctx cancellation to return.
	prog := newHeadlessProgram(silentModel{})

	p := NewTUIOperatorPrompter()
	p.Attach(prog)

	runDone := make(chan error, 1)
	go func() { _, _ = prog.Run(); runDone <- nil }()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := p.Ask(ctx, "sid", []host.OperatorQuestion{{Question: "q"}})
		errCh <- err
	}()
	cancel()

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Ask did not return on ctx cancel")
	}
	prog.Quit()
	<-runDone
}

// silentModel ignores every message, so a forwarded question is never answered.
type silentModel struct{}

func (silentModel) Init() tea.Cmd                       { return nil }
func (silentModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return silentModel{}, nil }
func (silentModel) View() string                        { return "" }
