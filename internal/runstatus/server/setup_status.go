package server

import "strings"

// SetupWarning is an operator-facing first-start setup warning surfaced by the
// web home screen before any session exists. It must not include secrets.
type SetupWarning struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Body          string `json:"body"`
	ActionLabel   string `json:"action_label,omitempty"`
	ActionCommand string `json:"action_command,omitempty"`
	StoryID       string `json:"story_id,omitempty"`
	StoryRef      string `json:"story_ref,omitempty"`
}

func cleanSetupWarnings(in []SetupWarning) []SetupWarning {
	if len(in) == 0 {
		return nil
	}
	out := make([]SetupWarning, 0, len(in))
	for _, w := range in {
		w.ID = strings.TrimSpace(w.ID)
		w.Title = strings.TrimSpace(w.Title)
		w.Body = strings.TrimSpace(w.Body)
		w.ActionLabel = strings.TrimSpace(w.ActionLabel)
		w.ActionCommand = strings.TrimSpace(w.ActionCommand)
		w.StoryID = strings.TrimSpace(w.StoryID)
		w.StoryRef = strings.TrimSpace(w.StoryRef)
		if w.ID == "" || w.Title == "" || w.Body == "" {
			continue
		}
		out = append(out, w)
	}
	return out
}

func (s *Server) setupStatus() any {
	return map[string]any{
		"warnings":          append([]SetupWarning(nil), s.setupWarnings...),
		"project_onboarded": s.projectOnboarded,
	}
}
