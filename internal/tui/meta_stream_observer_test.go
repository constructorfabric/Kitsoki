// meta_stream_observer_test.go — proves OnStreamEvent dispatches a
// MetaStreamMsg into the bound tea.Program's message channel.
//
// Scope is deliberately narrow: this is the producer→consumer wire
// test. We don't drive the full agent-runner stream through it;
// that integration is implicit through host.WithStreamSink's
// nil-safe contract and the existing agent runner tests.
package tui_test

import (
	"bytes"
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	tuipkg "kitsoki/internal/tui"
)

// streamCaptureModel records every non-Quit message Update sees, then
// quits as soon as it spots a sentinel value the test injects post-
// dispatch. Same shape as observer_test.go's captureModel — copied
// rather than shared because the sentinel type is package-private
// here too.
type streamCaptureModel struct {
	mu   *sync.Mutex
	msgs *[]tea.Msg
	done chan struct{}
}

func newStreamCaptureModel() (streamCaptureModel, *sync.Mutex, *[]tea.Msg, chan struct{}) {
	mu := &sync.Mutex{}
	msgs := &[]tea.Msg{}
	done := make(chan struct{})
	return streamCaptureModel{mu: mu, msgs: msgs, done: done}, mu, msgs, done
}

func (m streamCaptureModel) Init() tea.Cmd { return nil }

func (m streamCaptureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.QuitMsg); ok {
		return m, tea.Quit
	}
	m.mu.Lock()
	*m.msgs = append(*m.msgs, msg)
	hasSentinel := false
	for _, recorded := range *m.msgs {
		if _, ok := recorded.(streamCaptureSentinel); ok {
			hasSentinel = true
			break
		}
	}
	m.mu.Unlock()
	if hasSentinel {
		select {
		case <-m.done:
		default:
			close(m.done)
		}
		return m, tea.Quit
	}
	return m, nil
}

func (m streamCaptureModel) View() string { return "" }

// streamCaptureSentinel is the test-injected marker that signals "the
// dispatch under test has either fired or definitely won't fire" —
// dispatching is synchronous-ish through a goroutine, so by the time
// our sentinel reaches Update any earlier OnStreamEvent's goroutine
// has had ample opportunity to complete its prog.Send.
type streamCaptureSentinel struct{}

// TestMetaStreamSink_DispatchesEventAsMsg wires a MetaStreamSink to a
// headless tea.Program and asserts that OnStreamEvent results in a
// MetaStreamMsg arriving in the program's message loop.
//
// Why we can match MetaStreamMsg directly here: it's an EXPORTED type
// in package tui (the message must cross the tui→host boundary, so it
// can't be unexported the way turnOutcomeMsg is). That lets us assert
// on the payload rather than counting "any non-Quit message" the way
// observer_test.go has to.
func TestMetaStreamSink_DispatchesEventAsMsg(t *testing.T) {
	model, _, msgs, done := newStreamCaptureModel()
	var stdout bytes.Buffer
	prog := tea.NewProgram(model,
		tea.WithInput(&bytes.Buffer{}),
		tea.WithOutput(&stdout),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
	)

	sink := tuipkg.NewMetaStreamSink()
	sink.Attach(prog)
	t.Cleanup(sink.Detach)

	runDone := make(chan error, 1)
	go func() {
		_, runErr := prog.Run()
		runDone <- runErr
	}()
	// Give the program a moment to start its message loop so Send
	// doesn't race with goroutine launch.
	time.Sleep(50 * time.Millisecond)

	// Fire one stream event — this is what runClaudeStreamJSON would
	// do per JSONL line claude emits.
	want := host.StreamEvent{
		Type:    "assistant",
		Tool:    "Read",
		Preview: "prompt.md",
	}
	sink.OnStreamEvent(context.Background(), want)

	// The dispatch is fire-and-forget through a goroutine, so we
	// can't guarantee it lands before our subsequent synchronous
	// Send. Give the dispatch goroutine a moment to enqueue the
	// MetaStreamMsg before we inject the sentinel, otherwise the
	// sentinel can race ahead and trigger Quit prematurely.
	time.Sleep(50 * time.Millisecond)

	// Inject the sentinel AFTER OnStreamEvent so any preceding
	// MetaStreamMsg from the sink's goroutine reaches Update first.
	// The sentinel triggers a Quit.
	prog.Send(streamCaptureSentinel{})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamCaptureModel.Update never saw the sentinel — program message loop not running")
	}
	prog.Quit()

	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("tea.Program did not exit after Quit")
	}

	// Search the captured msg log for our MetaStreamMsg with the
	// expected payload.
	var found bool
	for _, m := range *msgs {
		sm, ok := m.(tuipkg.MetaStreamMsg)
		if !ok {
			continue
		}
		if reflect.DeepEqual(sm.Event, want) {
			found = true
			break
		}
	}
	require.True(t, found,
		"OnStreamEvent should have delivered a MetaStreamMsg with the expected event; got msgs=%v", *msgs)
}

// TestMetaStreamSink_DetachStopsDispatch asserts that after Detach()
// OnStreamEvent becomes a no-op. We can't *prove* a negative within
// a finite test window, but we can show the sentinel reaches the
// program without any MetaStreamMsg preceding it.
func TestMetaStreamSink_DetachStopsDispatch(t *testing.T) {
	model, _, msgs, done := newStreamCaptureModel()
	var stdout bytes.Buffer
	prog := tea.NewProgram(model,
		tea.WithInput(&bytes.Buffer{}),
		tea.WithOutput(&stdout),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
		tea.WithoutCatchPanics(),
	)

	sink := tuipkg.NewMetaStreamSink()
	sink.Attach(prog)

	runDone := make(chan error, 1)
	go func() {
		_, runErr := prog.Run()
		runDone <- runErr
	}()
	time.Sleep(50 * time.Millisecond)

	sink.Detach()
	// This call should be a no-op (the sink was detached above).
	sink.OnStreamEvent(context.Background(), host.StreamEvent{Type: "assistant", Preview: "should-not-appear"})

	// Give any (incorrectly dispatched) goroutine a chance to land
	// before we shut down — without this delay the test would pass
	// even if Detach hadn't actually stopped dispatch, because the
	// sentinel races ahead.
	time.Sleep(50 * time.Millisecond)

	// Sentinel-triggered shutdown so we can assert on what was
	// observed.
	prog.Send(streamCaptureSentinel{})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamCaptureModel.Update never saw the sentinel — program message loop not running")
	}
	prog.Quit()
	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		t.Fatal("tea.Program did not exit after Quit")
	}

	for _, m := range *msgs {
		if _, ok := m.(tuipkg.MetaStreamMsg); ok {
			t.Fatalf("MetaStreamMsg should not have been dispatched after Detach; got msgs=%v", *msgs)
		}
	}
}

// TestNewMetaStreamSink_NilSafeOps asserts that the zero-value
// operations (no Attach, then OnStreamEvent) don't panic and don't
// dispatch anything. The metaSendCmd path passes a (possibly nil)
// sink through host.WithStreamSink — both layers are nil-safe — so
// a sink that was never attached MUST also be quietly inert.
func TestNewMetaStreamSink_NilSafeOps(t *testing.T) {
	sink := tuipkg.NewMetaStreamSink()
	// No Attach. OnStreamEvent must not panic and must not dispatch.
	sink.OnStreamEvent(context.Background(), host.StreamEvent{Type: "assistant"})
	// Detach without Attach must not panic either.
	sink.Detach()
}
