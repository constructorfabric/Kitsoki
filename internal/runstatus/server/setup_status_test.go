package server_test

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/runstatus/server"
)

func TestSetupStatusReturnsConfiguredWarnings(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(server.NewMulti(newStubProvider(),
		server.WithSetupWarnings([]server.SetupWarning{
			{
				ID:            " run-as-user ",
				Title:         " Delegated agent user missing ",
				Body:          " Run the setup story before allowing agent writes. ",
				ActionLabel:   " Open setup story ",
				ActionCommand: " kitsoki run @kitsoki/run-as-user-setup ",
				StoryID:       " run-as-user-setup ",
				StoryRef:      " @kitsoki/run-as-user-setup ",
			},
			{ID: "broken", Title: "missing body"},
		}),
		server.WithProjectOnboarded(true),
	).Handler())
	defer ts.Close()

	var out struct {
		Warnings         []server.SetupWarning `json:"warnings"`
		ProjectOnboarded bool                  `json:"project_onboarded"`
	}
	rpcCall(t, ts, "runstatus.setup.status", map[string]any{}, &out)

	require.True(t, out.ProjectOnboarded)
	require.Len(t, out.Warnings, 1)
	require.Equal(t, server.SetupWarning{
		ID:            "run-as-user",
		Title:         "Delegated agent user missing",
		Body:          "Run the setup story before allowing agent writes.",
		ActionLabel:   "Open setup story",
		ActionCommand: "kitsoki run @kitsoki/run-as-user-setup",
		StoryID:       "run-as-user-setup",
		StoryRef:      "@kitsoki/run-as-user-setup",
	}, out.Warnings[0])
}
