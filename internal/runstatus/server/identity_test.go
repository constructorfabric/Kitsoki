package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/jobs"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus"
)

// captureDriver records the slots it was last driven with, so a test can prove
// the server injected the resolved operator identity as slots.author. This is a
// white-box test (package server) because the Driver interface returns the
// unexported intentInfo type, which a black-box fake cannot satisfy.
type captureDriver struct{ lastSlots map[string]any }

func (d *captureDriver) Turn(context.Context, string) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (d *captureDriver) SubmitDirect(_ context.Context, _ string, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	d.lastSlots = slots
	return &orchestrator.TurnOutcome{}, nil
}
func (d *captureDriver) ContinueTurn(_ context.Context, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	d.lastSlots = slots
	return &orchestrator.TurnOutcome{}, nil
}
func (d *captureDriver) AskOffPath(context.Context, string) (string, error) { return "", nil }
func (d *captureDriver) View(context.Context) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (d *captureDriver) IntentInfo(string, string) (intentInfo, bool)     { return intentInfo{}, false }
func (d *captureDriver) DefaultIntent(string) string                      { return "" }
func (d *captureDriver) PatchWorld(context.Context, map[string]any) error { return nil }
func (d *captureDriver) ListNotifications(context.Context) ([]jobs.Notification, error) {
	return nil, nil
}
func (d *captureDriver) MarkNotificationRead(context.Context, string) error { return nil }
func (d *captureDriver) DismissNotification(context.Context, string) error  { return nil }
func (d *captureDriver) Teleport(context.Context, string) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (d *captureDriver) RewindRoute(context.Context, string, orchestrator.ContextRouteClass, string, string) (*orchestrator.TurnOutcome, error) {
	return &orchestrator.TurnOutcome{}, nil
}
func (d *captureDriver) RecordRoutingFeedback(context.Context, string, string, string, string, orchestrator.RoutingFeedbackVerdict) error {
	return nil
}

// stubSource is a do-nothing Source — the identity tests only exercise the
// write RPCs, which read entry.Driver, never entry.Source.
type stubSource struct{ def *app.AppDef }

func (s stubSource) Snapshot() (runstatus.Snapshot, error)   { return runstatus.Snapshot{}, nil }
func (s stubSource) Events() ([]runstatus.TraceEvent, error) { return nil, nil }
func (s stubSource) AppDef() *app.AppDef                     { return s.def }

func buildIdentityServer(t *testing.T, defaultActor string) (*httptest.Server, *captureDriver) {
	t.Helper()
	drv := &captureDriver{}
	opts := []Option{WithDriver(drv)}
	if defaultActor != "" {
		opts = append(opts, WithDefaultActor(defaultActor))
	}
	ts := httptest.NewServer(NewWithSource(stubSource{def: &app.AppDef{}}, opts...).Handler())
	t.Cleanup(ts.Close)
	return ts, drv
}

func rpcPost(t *testing.T, url, method string, params map[string]any, headers map[string]string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url+"/rpc", strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
}

func TestIdentity_DefaultActorInjected(t *testing.T) {
	t.Parallel()
	ts, drv := buildIdentityServer(t, "alice")
	rpcPost(t, ts.URL, "runstatus.session.submit", map[string]any{"intent": "go"}, nil)
	require.NotNil(t, drv.lastSlots)
	assert.Equal(t, "alice", drv.lastSlots["author"])
}

func TestIdentity_NoIdentityLeavesSlotsUntouched(t *testing.T) {
	t.Parallel()
	ts, drv := buildIdentityServer(t, "")
	rpcPost(t, ts.URL, "runstatus.session.submit", map[string]any{"intent": "go"}, nil)
	_, present := drv.lastSlots["author"]
	assert.False(t, present, "with no identity source the author slot must be absent")
}

func TestIdentity_HeaderBeatsDefault(t *testing.T) {
	t.Parallel()
	ts, drv := buildIdentityServer(t, "alice")
	rpcPost(t, ts.URL, "runstatus.session.submit", map[string]any{"intent": "go"},
		map[string]string{"X-Kitsoki-Actor": "bob"})
	assert.Equal(t, "bob", drv.lastSlots["author"])
}

func TestIdentity_PrecedenceHeaderActorDefault(t *testing.T) {
	t.Parallel()
	ts, drv := buildIdentityServer(t, "alice")

	rpcPost(t, ts.URL, "runstatus.session.continue", map[string]any{"actor": "carol"}, nil)
	assert.Equal(t, "carol", drv.lastSlots["author"], "actor param beats default")

	rpcPost(t, ts.URL, "runstatus.session.continue", map[string]any{"actor": "carol"},
		map[string]string{"X-Kitsoki-Actor": "dave"})
	assert.Equal(t, "dave", drv.lastSlots["author"], "header beats actor param")
}

func TestIdentity_ExplicitAuthorSlotWins(t *testing.T) {
	t.Parallel()
	ts, drv := buildIdentityServer(t, "alice")
	rpcPost(t, ts.URL, "runstatus.session.submit",
		map[string]any{"intent": "go", "slots": map[string]any{"author": "typed-author"}}, nil)
	assert.Equal(t, "typed-author", drv.lastSlots["author"])
}
