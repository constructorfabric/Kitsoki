package tui

import (
	"io"
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

type recordingTerminalOutputSender struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (s *recordingTerminalOutputSender) Send(msg tea.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
}

func (s *recordingTerminalOutputSender) texts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var texts []string
	for _, msg := range s.msgs {
		if output, ok := msg.(terminalOutputMsg); ok {
			texts = append(texts, output.text)
		}
	}
	return texts
}

func TestTerminalOutputCaptureDropsCursorOnlyWrites(t *testing.T) {
	capture, err := NewTerminalOutputCapture()
	require.NoError(t, err)
	sender := &recordingTerminalOutputSender{}
	capture.Attach(sender)

	_, err = io.WriteString(capture.Writer(), "\n\x1b[?25l\x1b[?25h\n")
	require.NoError(t, err)
	require.NoError(t, capture.Close())
	require.Empty(t, sender.texts(),
		"blank/control-only output must be swallowed before it can move the live TUI cursor")
}

func TestTerminalOutputCaptureRoutesVisibleLinesThroughTea(t *testing.T) {
	capture, err := NewTerminalOutputCapture()
	require.NoError(t, err)
	sender := &recordingTerminalOutputSender{}
	capture.Attach(sender)

	_, err = io.WriteString(capture.Writer(), "first external line\n\x1b[31msecond line\x1b[0m")
	require.NoError(t, err)
	require.NoError(t, capture.Close())
	require.Equal(t, []string{"first external line", "second line"}, sender.texts())
}

func TestTerminalOutputMessageSurvivesInteractiveModes(t *testing.T) {
	for _, mode := range []Mode{ModeMenu, ModeChoosing, ModeOperatorQuestion} {
		t.Run(modeLabel(mode), func(t *testing.T) {
			m := RootModel{
				mode:       mode,
				transcript: newTranscriptModel(80, 20),
			}

			next, cmd := m.Update(terminalOutputMsg{text: "helper warning"})
			rm, ok := next.(RootModel)
			require.True(t, ok)
			require.Contains(t, rm.transcript.AllContent(), "process: helper warning")
			require.NotNil(t, cmd, "captured visible output should flush through tea.Println")
		})
	}
}
