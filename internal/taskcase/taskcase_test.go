package taskcase

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadAndValidateAgentEvalPilot(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "tools", "history-training", "examples", "git-ops-commit-message-agent-eval.yaml"))
	require.NoError(t, err)
	result := Validate(c)
	require.Empty(t, result.Errors)
	require.Equal(t, LaneAgentEval, c.Lane)
	require.Equal(t, OracleAgentEval, c.Oracle.Kind)
}

func TestBugfixManifestsAdaptToHistoryTaskCases(t *testing.T) {
	for _, project := range []string{"query-string", "kitsoki", "gears-rust"} {
		t.Run(project, func(t *testing.T) {
			path := filepath.Join("..", "..", "tools", "bugfix-bakeoff", "external", "projects", project, "manifest.yaml")
			cases, err := LoadBugfixManifest(path, nil)
			require.NoError(t, err)
			require.NotEmpty(t, cases)
			for _, c := range cases {
				result := Validate(&c)
				require.Empty(t, result.Errors, "%s: %#v", c.ID, result.Errors)
				require.Equal(t, LaneBugfix, c.Lane)
				require.Equal(t, OracleRedGreen, c.Oracle.Kind)
				require.Contains(t, c.Oracle.Command, "--project "+project)
			}
		})
	}
}

func TestBugfixAdapterScopesSelectedBugs(t *testing.T) {
	path := filepath.Join("..", "..", "tools", "bugfix-bakeoff", "external", "projects", "query-string", "manifest.yaml")
	cases, err := LoadBugfixManifest(path, []string{"qs2"})
	require.NoError(t, err)
	require.Len(t, cases, 1)
	require.Equal(t, "query-string-qs2", cases[0].ID)
}

func TestValidateRejectsUnarmedAutonomousCase(t *testing.T) {
	c := Case{
		Kind: Kind,
		ID:   "docs-case",
		Lane: LaneDocs,
		Source: Source{
			CorpusRef: "manual:case",
			Repo:      ".",
		},
		Story: Story{
			App:        "stories/docs-review/app.yaml",
			Entrypoint: "review",
		},
		TrainableSurface: TrainableSurface{WeightKind: WeightPrompt},
		Input:            Input{PromptOrTicket: "review docs"},
		Oracle:           Oracle{Kind: OracleStaticCheck},
		CostPolicy:       CostPolicy{LivePolicy: LiveNoCost},
		Artifacts:        Artifacts{Root: ".artifacts/history-training/docs-case/"},
	}
	result := Validate(&c)
	require.Contains(t, result.Errors, "oracle requires command or comparator unless kind is human_review_required")
}
