package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/jobs"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/world"
)

// supplementCaptureDriver records the supplemental slots lifted onto the ctx for
// a free-text Turn — the seam by which the deck slide the operator is viewing
// (`current_scene`) reaches the routed intent with no annotation.
type supplementCaptureDriver struct{ got world.Slots }

func (d *supplementCaptureDriver) Turn(ctx context.Context, _ string) (*orchestrator.TurnOutcome, error) {
	d.got = turnSupplementsFromCtx(ctx)
	return &orchestrator.TurnOutcome{Mode: orchestrator.ModeTransitioned, TurnNumber: 1}, nil
}
func (d *supplementCaptureDriver) SubmitDirect(context.Context, string, map[string]any) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (d *supplementCaptureDriver) ContinueTurn(context.Context, map[string]any) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (d *supplementCaptureDriver) AskOffPath(context.Context, string) (string, error) {
	return "", nil
}
func (d *supplementCaptureDriver) View(context.Context) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (d *supplementCaptureDriver) IntentInfo(string, string) (intentInfo, bool) {
	return intentInfo{}, false
}
func (d *supplementCaptureDriver) DefaultIntent(string) string { return "" }
func (d *supplementCaptureDriver) PatchWorld(context.Context, map[string]any) error {
	return nil
}
func (d *supplementCaptureDriver) ListNotifications(context.Context) ([]jobs.Notification, error) {
	return nil, nil
}
func (d *supplementCaptureDriver) MarkNotificationRead(context.Context, string) error { return nil }
func (d *supplementCaptureDriver) DismissNotification(context.Context, string) error  { return nil }
func (d *supplementCaptureDriver) Teleport(context.Context, string) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (d *supplementCaptureDriver) RewindRoute(context.Context, string, orchestrator.ContextRouteClass, string) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (d *supplementCaptureDriver) RecordRoutingFeedback(context.Context, string, string, string, string, orchestrator.RoutingFeedbackVerdict) error {
	return nil
}

func TestTurnStream_FreeTextLiftsViewSlots(t *testing.T) {
	drv := &supplementCaptureDriver{}
	src := routingFrameSource{def: &app.AppDef{}}
	ts := httptest.NewServer(NewWithSource(src, WithDriver(drv)).Handler())
	defer ts.Close()

	body, err := json.Marshal(map[string]any{
		"method": "turn",
		"input":  "make the title bolder",
		"slots":  map[string]any{"current_scene": "9"},
	})
	require.NoError(t, err)

	resp, err := http.Post(ts.URL+"/rpc/turn-stream", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// Drain the SSE stream so the turn goroutine runs to completion.
	buf := make([]byte, 4096)
	for {
		if _, e := resp.Body.Read(buf); e != nil {
			break
		}
	}

	require.Equal(t, "9", drv.got["current_scene"],
		"the viewed slide must ride the free-text turn as a current_scene supplement")
}
