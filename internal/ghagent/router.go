package ghagent

import (
	"strings"
)

// StoryPRBeat is the sentinel story value for the minimal PR autopilot beat.
// The full stories/pr-autopilot does not exist in round 1; the Dispatcher's
// spawn branch recognises this sentinel and runs the single pr_status-read +
// status-comment beat instead of loading a story app.yaml.
const StoryPRBeat = "pr-beat"

// StoryPRRebase is the sentinel story value for autonomous PR rebase/conflict
// resolution requests.
const StoryPRRebase = "pr-rebase"

// Route is one label->story mapping: the story app.yaml path (or the StoryPRBeat
// sentinel) plus world seed keys merged into the spawned session's
// initial_world.
type Route struct {
	Story       string
	World       map[string]any
	AppPath     string
	ProjectRoot string
	BeatFixture string
}

// LabelStoryMap is the configured router (epic: "configured, not hard-coded").
type LabelStoryMap map[string]Route

// DefaultLabelStoryMap is the round-1 default table:
//
//	bug     -> stories/bugfix    (judge_mode: llm_then_human)
//	feature -> stories/dev-story  (ticket_type: feature)
//	pr        -> the minimal pr-autopilot beat
//	pr_rebase -> the hosted PR rebase/conflict-resolution beat
func DefaultLabelStoryMap() LabelStoryMap {
	return LabelStoryMap{
		"bug":       {Story: "stories/bugfix", World: map[string]any{"judge_mode": "llm_then_human"}},
		"feature":   {Story: "stories/dev-story", World: map[string]any{"ticket_type": "feature"}},
		"pr":        {Story: StoryPRBeat, World: map[string]any{}},
		"pr_rebase": {Story: StoryPRRebase, World: map[string]any{}},
	}
}

// Classify maps a mention to a Route. PR objects route to the "pr" entry
// regardless of label; issues classify only on explicit bug/feature/epic labels
// or title markers. Unlike host.GHClassifyType, this intentionally does not
// default ambiguous issues to bug: a live @kitsoki mention with no clear signal
// should ask for guidance rather than guess.
func (m LabelStoryMap) Classify(mention Mention, labels []string) (Route, bool) {
	if mention.Item.Kind == "pr" {
		if wantsPRRebase(mention.Item.Title) {
			if r, ok := m["pr_rebase"]; ok {
				return r, true
			}
		}
		r, ok := m["pr"]
		return r, ok
	}
	class := classifyMentionIssue(labels, mention.Item.Title)
	if class == "" {
		return Route{}, false
	}
	r, ok := m[class]
	return r, ok
}

func wantsPRRebase(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return false
	}
	if strings.Contains(t, "rebase") {
		return true
	}
	hasConflict := strings.Contains(t, "conflict")
	hasResolve := strings.Contains(t, "resolve") || strings.Contains(t, "fix")
	return hasConflict && hasResolve
}

func classifyMentionIssue(labels []string, title string) string {
	for _, label := range labels {
		switch strings.ToLower(strings.TrimSpace(label)) {
		case "bug", "kind:bug", "type:bug":
			return "bug"
		case "feature", "enhancement", "kind:feature", "type:feature":
			return "feature"
		case "epic", "kind:epic", "type:epic":
			return "epic"
		}
	}
	switch t := strings.ToLower(strings.TrimSpace(title)); {
	case strings.HasPrefix(t, "bug:") || strings.Contains(t, "[bug]"):
		return "bug"
	case strings.HasPrefix(t, "feature:") || strings.Contains(t, "[feature]"):
		return "feature"
	case strings.HasPrefix(t, "epic:") || strings.Contains(t, "[epic]"):
		return "epic"
	default:
		return ""
	}
}
