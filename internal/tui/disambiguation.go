package tui

import (
	"fmt"
	"strconv"
	"strings"

	"kitsoki/internal/intent"
	"kitsoki/internal/tui/blocks"
)

// disambiguationModel tracks the state of an in-progress disambiguation
// (§7.4). It no longer owns the prompt area — the user types a number
// or the canonical intent name into the normal textarea, and the inline
// "Did you mean?" block is rendered into the transcript. The legacy
// numeric-hotkey path has been removed; selection goes through the
// normal prompt's Enter + Submit pipeline.
type disambiguationModel struct {
	active     bool
	candidates []intent.Candidate
}

func newDisambiguationModel() disambiguationModel {
	return disambiguationModel{}
}

// Open activates the disambiguation model with the given candidates.
func (m *disambiguationModel) Open(candidates []intent.Candidate) {
	m.active = true
	m.candidates = candidates
}

// Close deactivates the model.
func (m *disambiguationModel) Close() {
	m.active = false
	m.candidates = nil
}

// IsActive reports whether the model is currently presenting a pick.
func (m *disambiguationModel) IsActive() bool { return m.active }

// Candidates returns the current candidate list (nil when inactive).
func (m *disambiguationModel) Candidates() []intent.Candidate { return m.candidates }

// disambiguationChoiceMsg is sent when the user picks a candidate.
type disambiguationChoiceMsg struct {
	chosen intent.Candidate
}

// RenderInlineBlock returns the styled "Did you mean?" transcript block
// for the current candidate list, ready to pass to
// transcript.AppendBlock. Returns empty when no candidates are active.
func (m *disambiguationModel) RenderInlineBlock(r *blocks.Renderer) string {
	if !m.active || len(m.candidates) == 0 {
		return ""
	}
	cs := make([]blocks.DisambigCandidate, len(m.candidates))
	for i, c := range m.candidates {
		cs[i] = blocks.DisambigCandidate{
			Intent:      c.Intent,
			Title:       c.Title,
			Description: c.Description,
			Why:         c.Why,
		}
	}
	return r.Disambig(cs)
}

// SubmitValue accepts a typed value and returns the chosen candidate.
// Accepts either a 1-based index ("2") or the canonical intent name
// (case-insensitive). Invalid picks return an error so the caller can
// surface a hint in the transcript and leave the model intact for
// retry. On success the model is left active; the caller invokes
// Close() after dispatching the chosen candidate.
func (m *disambiguationModel) SubmitValue(input string) (intent.Candidate, error) {
	if !m.active {
		return intent.Candidate{}, fmt.Errorf("disambig: not active")
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return intent.Candidate{}, fmt.Errorf("disambig: empty value")
	}
	if n, err := strconv.Atoi(input); err == nil {
		if n < 1 || n > len(m.candidates) {
			return intent.Candidate{}, fmt.Errorf("disambig: choice %d out of range (1..%d)", n, len(m.candidates))
		}
		return m.candidates[n-1], nil
	}
	for _, c := range m.candidates {
		if strings.EqualFold(c.Intent, input) {
			return c, nil
		}
		if c.Title != "" && strings.EqualFold(c.Title, input) {
			return c, nil
		}
	}
	return intent.Candidate{}, fmt.Errorf("disambig: %q does not match any candidate", input)
}
