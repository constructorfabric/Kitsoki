package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

func kmsg(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }
func rmsg(s string) tea.KeyMsg      { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// drive feeds a sequence of keys through the model, returning the final model
// and the first non-nil result encountered (the commit/cancel signal).
func drive(m operatorQuestionModel, keys ...tea.KeyType) (operatorQuestionModel, *operatorQuestionResult) {
	var res *operatorQuestionResult
	for _, k := range keys {
		var r *operatorQuestionResult
		m, _, r = m.Update(kmsg(k))
		if r != nil {
			res = r
		}
	}
	return m, res
}

func driveMsgs(m operatorQuestionModel, msgs ...tea.Msg) (operatorQuestionModel, *operatorQuestionResult) {
	var res *operatorQuestionResult
	for _, msg := range msgs {
		var r *operatorQuestionResult
		m, _, r = m.Update(msg)
		if r != nil {
			res = r
		}
	}
	return m, res
}

func TestOperatorQuestion_OpenRejectsEmptyBatch(t *testing.T) {
	var m operatorQuestionModel
	err := m.Open(nil, make(chan map[string]any, 1))
	require.Error(t, err)
	assert.False(t, m.IsActive())
}

func TestOperatorQuestion_SingleSelectAnswersByLabel(t *testing.T) {
	var m operatorQuestionModel
	require.NoError(t, m.Open([]host.OperatorQuestion{{
		Question: "Ship it?",
		Header:   "Ship",
		Options:  []host.OperatorOption{{Label: "Yes"}, {Label: "No"}},
	}}, make(chan map[string]any, 1)))

	// Cursor down to "No", then Enter.
	_, res := drive(m, tea.KeyDown, tea.KeyEnter)
	require.NotNil(t, res)
	assert.False(t, res.Cancel)
	assert.Equal(t, map[string]any{"Ship it?": "No"}, res.Answers)
}

func TestOperatorQuestion_SingleSelectCustomAnswer(t *testing.T) {
	var m operatorQuestionModel
	require.NoError(t, m.Open([]host.OperatorQuestion{{
		Question: "Ship it?",
		Header:   "Ship",
		Options:  []host.OperatorOption{{Label: "Yes"}, {Label: "No"}},
	}}, make(chan map[string]any, 1)))

	// Cursor down past the listed answers to Custom answer, type text, then Enter.
	m, res := drive(m, tea.KeyDown, tea.KeyDown)
	require.Nil(t, res)
	_, res = driveMsgs(m, rmsg("Canary"), kmsg(tea.KeySpace), rmsg("after"), kmsg(tea.KeySpace), rmsg("checks"), kmsg(tea.KeyEnter))

	require.NotNil(t, res)
	assert.False(t, res.Cancel)
	assert.Equal(t, map[string]any{"Ship it?": "Canary after checks"}, res.Answers)
}

func TestOperatorQuestion_SingleSelectCustomAnswerRequiresText(t *testing.T) {
	var m operatorQuestionModel
	require.NoError(t, m.Open([]host.OperatorQuestion{{
		Question: "Ship it?",
		Header:   "Ship",
		Options:  []host.OperatorOption{{Label: "Yes"}, {Label: "No"}},
	}}, make(chan map[string]any, 1)))

	m, res := drive(m, tea.KeyDown, tea.KeyDown, tea.KeyEnter)
	assert.Nil(t, res)
	assert.True(t, m.IsActive())
	assert.Contains(t, m.View(60), "type a custom answer")
}

func TestOperatorQuestion_SingleSelectRendersCustomAffordance(t *testing.T) {
	var m operatorQuestionModel
	require.NoError(t, m.Open([]host.OperatorQuestion{{
		Question: "Ship it?",
		Header:   "Ship",
		Options: []host.OperatorOption{
			{Label: "Yes", Description: "deploy now"},
			{Label: "No", Description: "stop"},
		},
	}}, make(chan map[string]any, 1)))

	analyzer := NewRenderingAnalyzer(t, m.View(70))
	analyzer.AssertContains("Custom answer")
	analyzer.AssertContains("type your own response")
	analyzer.AssertContains("type on Custom answer")
	analyzer.AssertLineSeparation("No", "Custom answer")
}

func TestOperatorQuestion_MultiSelectAnswersByLabelList(t *testing.T) {
	var m operatorQuestionModel
	require.NoError(t, m.Open([]host.OperatorQuestion{{
		Question:    "Which?",
		Header:      "Pick",
		MultiSelect: true,
		Options:     []host.OperatorOption{{Label: "A"}, {Label: "B"}, {Label: "C"}},
	}}, make(chan map[string]any, 1)))

	// Toggle A (cursor 0), down twice to C, toggle C, Enter.
	_, res := drive(m, tea.KeySpace, tea.KeyDown, tea.KeyDown, tea.KeySpace, tea.KeyEnter)
	require.NotNil(t, res)
	assert.Equal(t, map[string]any{"Which?": []string{"A", "C"}}, res.Answers)
}

func TestOperatorQuestion_MultiSelectRequiresOnePick(t *testing.T) {
	var m operatorQuestionModel
	require.NoError(t, m.Open([]host.OperatorQuestion{{
		Question:    "Which?",
		MultiSelect: true,
		Options:     []host.OperatorOption{{Label: "A"}},
	}}, make(chan map[string]any, 1)))

	// Enter with nothing selected → no commit, error surfaced, still active.
	m, res := drive(m, tea.KeyEnter)
	assert.Nil(t, res)
	assert.True(t, m.IsActive())
	assert.Contains(t, m.View(60), "select at least one")
}

func TestOperatorQuestion_MultiQuestionBatchWalksInOrder(t *testing.T) {
	var m operatorQuestionModel
	require.NoError(t, m.Open([]host.OperatorQuestion{
		{Question: "Q1", Options: []host.OperatorOption{{Label: "a1"}, {Label: "b1"}}},
		{Question: "Q2", MultiSelect: true, Options: []host.OperatorOption{{Label: "a2"}, {Label: "b2"}}},
	}, make(chan map[string]any, 1)))

	// Q1: pick b1 (down + Enter) — should advance, NOT commit yet.
	m, res := drive(m, tea.KeyDown, tea.KeyEnter)
	require.Nil(t, res)
	require.True(t, m.IsActive())
	assert.Contains(t, m.View(60), "Q2")
	assert.Contains(t, m.View(60), "(2/2)")

	// Q2: toggle a2, Enter → finalize both answers.
	_, res = drive(m, tea.KeySpace, tea.KeyEnter)
	require.NotNil(t, res)
	assert.Equal(t, map[string]any{
		"Q1": "b1",
		"Q2": []string{"a2"},
	}, res.Answers)
}

func TestOperatorQuestion_EscCancels(t *testing.T) {
	var m operatorQuestionModel
	require.NoError(t, m.Open([]host.OperatorQuestion{{
		Question: "Ship it?",
		Options:  []host.OperatorOption{{Label: "Yes"}},
	}}, make(chan map[string]any, 1)))

	_, res := drive(m, tea.KeyEsc)
	require.NotNil(t, res)
	assert.True(t, res.Cancel)
	assert.Nil(t, res.Answers)
}

func TestOperatorQuestion_InactiveModelIgnoresKeys(t *testing.T) {
	var m operatorQuestionModel // not Open'd
	m2, _, res := m.Update(kmsg(tea.KeyEnter))
	assert.Nil(t, res)
	assert.False(t, m2.IsActive())
}
