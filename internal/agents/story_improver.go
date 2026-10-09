package agents

import (
	_ "embed"

	"kitsoki/internal/reportcontract"
)

//go:embed story_improver.md
var storyImproverPrompt string

// storyImprover returns the builtin story-improver Agent: a read-only
// continuous-improvement reviewer for the running story and its recent trace.
// It is surfaced through the builtin `story.improve` meta mode.
func storyImprover() Agent {
	return Agent{
		Name:         NameStoryImprover,
		SystemPrompt: storyImproverPrompt,
		Model:        "",
		Tools:        reportcontract.ReadOnlyTools(),
	}
}
