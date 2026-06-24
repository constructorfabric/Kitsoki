package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestNotificationFeed_SSE drives the cross-session feed end to end at the
// transport layer: subscribe, fire a relay (the SessionObserver path) for a
// posted notification, and assert one runstatus.notification frame with the
// refreshed $inbox counts arrives on the SSE stream. No orchestrator/LLM — the
// relay is invoked directly, exactly as notifyBackgroundTurn would.
func TestNotificationFeed_SSE(t *testing.T) {
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	js, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)

	sid := app.SessionID("sess-1")
	n := &jobs.Notification{
		SessionID:     sid,
		CreatedAt:     time.Now(),
		Severity:      jobs.SeverityActionRequired,
		Title:         "needs you",
		TeleportState: "foyer",
		OriginKind:    "job",
	}
	require.NoError(t, js.InsertNotification(context.Background(), n))

	srv := newServer(&singleEntryProvider{}, serverConfig{poll: 20 * time.Millisecond})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Subscribe to the global feed.
	subID := srv.notifs.subscribe()
	require.NotEmpty(t, subID)

	resp, err := http.Get(ts.URL + "/rpc/notifications?subscription_id=" + subID)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	// Fire the relay exactly as the orchestrator's background-turn fan-out does.
	relay := &notificationRelay{buf: srv.notifs, sid: sid, jobs: js}
	relay.OnBackgroundTurn(sid, nil)

	frame := readNotifFrame(t, resp, 3*time.Second)
	require.NotNil(t, frame)
	assert.Equal(t, "runstatus.notification", frame["method"])
	params := frame["params"].(map[string]any)
	assert.Equal(t, "sess-1", params["session_id"])
	assert.Equal(t, float64(1), params["unread"])
	assert.Equal(t, float64(1), params["needs_attention"])
	notif := params["notification"].(map[string]any)
	assert.Equal(t, "needs you", notif["Title"])
}

// TestNotificationFeed_OrchestratorRegistration drives the REAL registration
// and fan-out path with no LLM: it builds a live orchestrator over a tiny app
// with a background effect, registers the relay via the server's own
// AttachSession (Notifier) seam — exactly as `kitsoki web`'s registry does —
// then drives a stubbed background job to a TERMINAL state. The orchestrator's
// own handleJobTerminal posts the completion notification and fires its
// notifyBackgroundTurn fan-out, which must invoke the registered relay and
// land a runstatus.notification frame on the shared buffer.
//
// Unlike TestNotificationFeed_SSE (which calls relay.OnBackgroundTurn
// directly), this proves AttachSession actually registers an observer on the
// orchestrator and that the relay is reached through the real fan-out. If
// AttachSession silently no-op'd, no frame would appear.
func TestNotificationFeed_OrchestratorRegistration(t *testing.T) {
	def := &app.AppDef{
		App:   app.AppMeta{ID: "notif-reg-test"},
		Root:  "init",
		Hosts: []string{"host.test.echo"},
		World: map[string]app.VarDef{
			"last_job_id": {Type: "string", Default: ""},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On: map[string][]app.Transition{
					"enter": {{Target: "work"}},
				},
			},
			"work": {
				View: app.LegacyView("work"),
				OnEnter: []app.Effect{
					{
						Invoke:     "host.test.echo",
						With:       map[string]any{"msg": "done"},
						Background: true,
						Bind:       map[string]string{"last_job_id": "job_id"},
					},
				},
			},
		},
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	st, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	js, err := jobs.NewJobStore(st.DB())
	require.NoError(t, err)
	sched := jobs.NewScheduler(js)

	reg := host.NewRegistry()
	reg.Register("host.test.echo", func(_ context.Context, args map[string]any) (host.Result, error) {
		msg, _ := args["msg"].(string)
		return host.Result{Data: map[string]any{"output": msg}}, nil
	})

	// nil harness: we drive the foreground turn via SubmitDirect, so no LLM.
	orch := orchestrator.New(def, m, st, nil,
		orchestrator.WithHostRegistry(reg),
		orchestrator.WithScheduler(sched),
		orchestrator.WithJobStore(js),
	)

	srv := newServer(&singleEntryProvider{}, serverConfig{poll: 20 * time.Millisecond})

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Register the relay through the server's real Notifier seam.
	srv.AttachSession(orch, sid, string(sid), js)

	// Subscribe AFTER attach so the seeded watermark precedes the frame.
	subID := srv.notifs.subscribe()
	sub := srv.notifs.lookup(subID)
	require.NotNil(t, sub)

	// Drive the foreground turn that fires the background effect (no routing).
	_, err = orch.SubmitDirect(ctx, sid, "enter", nil)
	require.NoError(t, err)

	// Let the background job reach terminal and the on_complete chain commit;
	// handleJobTerminal then posts the notification and fans out to observers.
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	require.NoError(t, sched.WaitIdle(waitCtx))
	require.NoError(t, orch.WaitListenerIdle(waitCtx, sid))

	frames, _ := srv.notifs.since(sub.sent)
	require.Len(t, frames, 1,
		"the registered relay must have appended exactly one frame via the orchestrator's fan-out")
	assert.Equal(t, string(sid), frames[0].SessionID)
	require.NotNil(t, frames[0].Notification,
		"frame must carry the freshly-posted completion notification re-read by the relay")
}

// TestNotificationRelay_PublicIDStampedNotSID is a regression test for the
// "publicID != orchestrator sid on the SSE frame" bug. The relay reads the
// session's notifications from the DB by the orchestrator's internal sid, but
// the browser routes/teleports on the registry's public id (publicID). Before
// the fix, OnBackgroundTurn stamped the frame's session_id AND the carried
// notification's SessionID with the un-routable orchestrator sid, so the
// browser's teleport hit "unknown session."
//
// Here publicID differs from the DB-backing sid. We post a notification under
// sid (so the row only resolves via a sid read), fire the relay, then assert:
//   - the row content (Title) resolves — proving the read used sid, and
//   - the frame's SessionID AND the carried notification's SessionID equal
//     publicID, NOT sid.
//
// If the frame were stamped with sid (the reverted bug), both equality checks
// against publicID fail.
func TestNotificationRelay_PublicIDStampedNotSID(t *testing.T) {
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	js, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)

	sid := app.SessionID("orchestrator-internal-sid")
	publicID := "public-registry-uuid"
	require.NotEqual(t, string(sid), publicID, "test premise: ids must diverge")

	// Post the notification under the orchestrator sid — the only id the row is
	// reachable by in the DB.
	n := &jobs.Notification{
		SessionID:     sid,
		CreatedAt:     time.Now(),
		Severity:      jobs.SeverityActionRequired,
		Title:         "needs you",
		TeleportState: "foyer",
		OriginKind:    "job",
	}
	require.NoError(t, js.InsertNotification(context.Background(), n))

	srv := newServer(&singleEntryProvider{}, serverConfig{poll: 20 * time.Millisecond})

	// Relay reads by sid but must present publicID on the wire.
	relay := &notificationRelay{buf: srv.notifs, sid: sid, publicID: publicID, jobs: js}
	relay.OnBackgroundTurn(sid, nil)

	frames, _ := srv.notifs.since(0)
	require.Len(t, frames, 1, "relay must append exactly one frame")
	f := frames[0]

	// The row content resolved — proving the DB read used sid (the row is not
	// reachable by publicID).
	require.NotNil(t, f.Notification, "frame must carry the re-read notification")
	assert.Equal(t, "needs you", f.Notification.Title,
		"content must resolve via the sid-backed read")

	// Both the frame's session id and the carried notification's session id must
	// present the browser-facing publicID, not the un-routable orchestrator sid.
	assert.Equal(t, publicID, f.SessionID,
		"frame session_id must be publicID, not the orchestrator sid")
	assert.Equal(t, app.SessionID(publicID), f.Notification.SessionID,
		"carried notification SessionID must be publicID, not the orchestrator sid")
	assert.NotEqual(t, sid, f.Notification.SessionID,
		"the un-routable orchestrator sid must not leak onto the frame")
}

// TestNotifBuffer_WatermarkAndCap covers the ring's watermark clamping when
// frames are evicted past the cap.
func TestNotifBuffer_WatermarkAndCap(t *testing.T) {
	b := newNotifBuffer()
	id := b.subscribe()
	sub := b.lookup(id)
	require.NotNil(t, sub)

	for i := 0; i < notifBufferCap+10; i++ {
		b.append(notificationFrame{SessionID: "s"})
	}
	frames, watermark := b.since(sub.sent)
	// The subscriber seeded at 0 should see only the retained tail, not panic
	// on the 10 dropped frames.
	assert.Len(t, frames, notifBufferCap)
	assert.Equal(t, notifBufferCap+10, watermark)
}

func readNotifFrame(t *testing.T, resp *http.Response, timeout time.Duration) map[string]any {
	t.Helper()
	ch := make(chan map[string]any, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err == nil {
				ch <- m
				return
			}
		}
		ch <- nil
	}()
	select {
	case m := <-ch:
		return m
	case <-time.After(timeout):
		t.Fatal("timed out waiting for notification frame")
		return nil
	}
}
