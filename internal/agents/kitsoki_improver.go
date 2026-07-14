package agents

import (
	_ "embed"

	"kitsoki/internal/reportcontract"
)

//go:embed kitsoki_improver.md
var kitsokiImproverPrompt string

// kitsokiImprover returns the builtin kitsoki-improver Agent: a read-only
// continuous-improvement reviewer for kitsoki engine runs. It is surfaced
// through the builtin `kitsoki.improve` meta mode.
func kitsokiImprover() Agent {
	return Agent{
		Name:         NameKitsokiImprover,
		SystemPrompt: kitsokiImproverPrompt,
		Model:        "",
		Tools:        reportcontract.ReadOnlyTools(),
		DefaultCwd:   "${KITSOKI_REPO}",
	}
}
