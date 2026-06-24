package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus"
)

type routingFrameSource struct {
	def *app.AppDef
}

func (s routingFrameSource) Snapshot() (runstatus.Snapshot, error) {
	return runstatus.Snapshot{
		Session: runstatus.SessionHeader{SessionID: "sess-routing"},
		App:     s.def,
	}, nil
}

func (s routingFrameSource) Events() ([]runstatus.TraceEvent, error) { return nil, nil }

func (s routingFrameSource) AppDef() *app.AppDef { return s.def }

type routingFrameDriver struct{}

func (routingFrameDriver) Turn(ctx context.Context, _ string) (*orchestrator.TurnOutcome, error) {
	if sink := host.StreamSinkFrom(ctx); sink != nil {
		sink.OnStreamEvent(ctx, host.StreamEvent{
			Type:      "routing",
			Turn:      3,
			Intent:    "core.work",
			RoutedBy:  "fallback",
			MatchType: "free_text",
		})
	}
	return &orchestrator.TurnOutcome{
		Mode:       orchestrator.ModeTransitioned,
		NewState:   app.StatePath("workbench"),
		View:       "Workbench ready",
		TurnNumber: 3,
	}, nil
}

func (routingFrameDriver) SubmitDirect(context.Context, string, map[string]any) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}

func (routingFrameDriver) ContinueTurn(context.Context, map[string]any) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}

func (routingFrameDriver) AskOffPath(context.Context, string) (string, error) { return "", nil }

func (routingFrameDriver) View(context.Context) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}

func (routingFrameDriver) IntentInfo(string, string) (intentInfo, bool) {
	return intentInfo{}, false
}

func (routingFrameDriver) DefaultIntent(string) string { return "" }

func (routingFrameDriver) PatchWorld(context.Context, map[string]any) error { return nil }

func (routingFrameDriver) ListNotifications(context.Context) ([]jobs.Notification, error) {
	return nil, nil
}

func (routingFrameDriver) MarkNotificationRead(context.Context, string) error { return nil }

func (routingFrameDriver) DismissNotification(context.Context, string) error { return nil }

func (routingFrameDriver) Teleport(context.Context, string) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (routingFrameDriver) RewindRoute(context.Context, string, orchestrator.ContextRouteClass, string) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}

func TestTurnStream_RoutingFramePrecedesDone(t *testing.T) {
	ts := httptest.NewServer(NewWithSource(routingFrameSource{def: &app.AppDef{}}, WithDriver(routingFrameDriver{})).Handler())
	defer ts.Close()

	body, err := json.Marshal(map[string]any{
		"method": "turn",
		"input":  "do this ad hoc thing",
	})
	require.NoError(t, err)

	resp, err := http.Post(ts.URL+"/rpc/turn-stream", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var frames []struct {
		Type      string `json:"type"`
		Turn      int64  `json:"turn"`
		Intent    string `json:"intent"`
		RoutedBy  string `json:"routed_by"`
		MatchType string `json:"match_type"`
	}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame struct {
			Type      string `json:"type"`
			Turn      int64  `json:"turn"`
			Intent    string `json:"intent"`
			RoutedBy  string `json:"routed_by"`
			MatchType string `json:"match_type"`
		}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame))
		frames = append(frames, frame)
	}
	require.NoError(t, sc.Err())
	require.Len(t, frames, 2)
	require.Equal(t, "routing", frames[0].Type)
	require.Equal(t, int64(3), frames[0].Turn)
	require.Equal(t, "core.work", frames[0].Intent)
	require.Equal(t, "fallback", frames[0].RoutedBy)
	require.Equal(t, "free_text", frames[0].MatchType)
	require.Equal(t, "done", frames[1].Type)
}
