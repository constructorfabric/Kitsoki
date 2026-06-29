// Package tui_test — inbox polling ticker clock-injection tests.
//
// These tests verify that scheduleInboxPoll uses the injectable clock.Clock
// rather than real wall time. A *clock.Fake is injected via WithTUIClock;
// the fake clock's After channel fires only when Fake.Advance is called, so
// the tests run without any real wall-clock waits.
package tui_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/clock"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	tuipkg "kitsoki/internal/tui"
)

// buildModelWithFakeClock builds a RootModel wired with a *clock.Fake and a
// real (but empty in-memory) JobStore so the inbox ticker is active.
// It uses the cloak-of-darkness app (available in testdata) for a minimal setup.
func buildModelWithFakeClock(t *testing.T, fakeClk *clock.Fake) (tea.Model, *jobs.JobStore) {
	t.Helper()

	// Load the cloak-of-darkness app for a minimal orchestrator.
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	mach, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	require.NoError(t, err)

	orch := orchestrator.New(def, mach, s, h)
	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	js, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)

	m := tuipkg.NewRootModel(orch, sid, "", "", tuipkg.WithJobStore(js), tuipkg.WithTUIClock(fakeClk))
	return m, js
}

// TestInboxClockInjection_WithTUIClock verifies that WithTUIClock stores the
// clock in the model and that scheduleInboxPoll uses it instead of real time.
func TestInboxClockInjection_WithTUIClock(t *testing.T) {
	start := time.Unix(0, 0)
	fakeClk := clock.NewFake(start)

	m, _ := buildModelWithFakeClock(t, fakeClk)

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok, "ExtractRootModel should succeed")
	require.NotNil(t, tuipkg.RootModelClock(rm), "clock should be set after WithTUIClock")
}

// TestInboxClockInjection_Init verifies that Init() schedules an inbox poll
// tick by returning a non-nil Cmd when a jobStore is wired.
func TestInboxClockInjection_Init(t *testing.T) {
	fakeClk := clock.NewFake(time.Unix(0, 0))
	m, _ := buildModelWithFakeClock(t, fakeClk)

	// Init() should return a Cmd (BatchMsg wrapping multiple inits).
	cmd := m.Init()
	require.NotNil(t, cmd, "Init() should return a non-nil Cmd when jobStore is set")
}

// TestInboxClockInjection_FakeAdvanceFiresTick verifies the core property:
// a tick cmd returned by scheduleInboxPoll fires inboxPollMsg only after
// the fake clock is advanced past the delay — no real-time wait needed.
func TestInboxClockInjection_FakeAdvanceFiresTick(t *testing.T) {
	fakeClk := clock.NewFake(time.Unix(0, 0))
	m, _ := buildModelWithFakeClock(t, fakeClk)

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)

	// Obtain a tick cmd for a 2-second delay.
	tickCmd := tuipkg.ScheduleInboxPollForTest(rm, 2*time.Second)
	require.NotNil(t, tickCmd, "scheduleInboxPoll should return a non-nil Cmd")

	// Run the cmd in a goroutine — it will block on fakeClk.After(2s).
	done := make(chan tea.Msg, 1)
	go func() { done <- tickCmd() }()

	// Before advance: the goroutine should still be blocked.
	// BlockUntil(1) waits until at least one waiter is registered on the
	// fake clock — giving us a race-free point to assert "not yet fired".
	fakeClk.BlockUntil(1)
	select {
	case <-done:
		t.Fatal("tick fired before clock was advanced")
	default:
		// Expected: still blocked.
	}

	// Advance the clock past the delay.
	fakeClk.Advance(2 * time.Second)

	// Now the tick should fire promptly.
	select {
	case msg := <-done:
		pollMsg := tuipkg.InboxPollMsg()
		require.IsType(t, pollMsg, msg, "tick cmd should return an inboxPollMsg")
	case <-time.After(1 * time.Second):
		t.Fatal("tick did not fire within 1s after fake clock advance")
	}
}

// TestInboxClockInjection_SyntheticPollMsg verifies that injecting an
// inboxPollMsg directly into Update triggers the poll and schedules the
// next tick (the model returns a non-nil Cmd from inboxPollMsg handling).
func TestInboxClockInjection_SyntheticPollMsg(t *testing.T) {
	fakeClk := clock.NewFake(time.Unix(0, 0))
	m, _ := buildModelWithFakeClock(t, fakeClk)

	// Post a synthetic inboxPollMsg to simulate the timer firing.
	updatedModel, cmd := m.Update(tuipkg.InboxPollMsg())
	require.NotNil(t, updatedModel, "Update must return a model")
	// The handler returns a new Cmd (the DB read + next tick schedule).
	// We don't execute it (that would need a real DB read), just confirm
	// it's non-nil, meaning the machinery is wired.
	require.NotNil(t, cmd, "handling inboxPollMsg should schedule a follow-up Cmd")
}

func TestInboxClockInjection_SyntheticPollSyncsGitHubOnThrottle(t *testing.T) {
	fakeClk := clock.NewFake(time.Unix(0, 0))
	m, js := buildModelWithFakeClock(t, fakeClk)

	calls := []string{}
	restore := host.SetExecRunnerForTest(func(_ context.Context, _ string, name string, args ...string) (string, string, int, error) {
		key := name + " " + strings.Join(args, " ")
		calls = append(calls, key)
		switch key {
		case "gh --version":
			return "gh version 2.x\n", "", 0, nil
		case "gh issue list --state open --assignee @me --limit 100 --json number,title,assignees,url":
			return `[{"number":7,"title":"Assigned issue","url":"https://github.com/acme/repo/issues/7","assignees":[{"login":"brad"}]}]`, "", 0, nil
		case "gh pr list --state open --search review-requested:@me --limit 100 --json number,title,author,url":
			return `[]`, "", 0, nil
		default:
			return "", "unexpected command: " + key, 1, nil
		}
	})
	defer restore()

	updated, cmd := m.Update(tuipkg.InboxPollMsg())
	require.NotNil(t, cmd)
	msg := inboxRefreshedFromCmd(t, cmd)
	m = updated
	m, _ = m.Update(msg)
	require.Len(t, calls, 3)

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	ns, err := js.ListNotifications(context.Background(), rm.SessionIDForTest(), 20)
	require.NoError(t, err)
	require.Len(t, ns, 1)
	require.Equal(t, "github:acme/repo/issue/7", ns[0].OriginRef)

	updated, cmd = m.Update(tuipkg.InboxPollMsg())
	require.NotNil(t, cmd)
	_ = cmd()
	m = updated
	require.Len(t, calls, 3, "second nearby inbox tick should not call gh again")

	fakeClk.Advance(5 * time.Minute)
	updated, cmd = m.Update(tuipkg.InboxPollMsg())
	require.NotNil(t, cmd)
	_ = cmd()
	m = updated
	require.Len(t, calls, 6, "tick after throttle interval should poll gh again")
}

func inboxRefreshedFromCmd(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	wantType := reflect.TypeOf(tuipkg.InboxRefreshedMsg(nil))
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, next := range batch {
			if next == nil {
				continue
			}
			nested := next()
			if reflect.TypeOf(nested) == wantType {
				return nested
			}
			if _, ok := nested.(tea.BatchMsg); ok {
				if refreshed := inboxRefreshedFromCmd(t, func() tea.Msg { return nested }); refreshed != nil {
					return refreshed
				}
				continue
			}
		}
		t.Fatalf("expected inboxRefreshed in batch, got %T", msg)
	}
	require.IsType(t, tuipkg.InboxRefreshedMsg(nil), msg)
	return msg
}
