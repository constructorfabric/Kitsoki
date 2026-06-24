package host

// Cassette slow-play tests.
//
// Slow-play is the REPLAY-only feature that streams a recorded agent-action
// transcript to an installed StreamSink, paced by the recorded per-event timings,
// so a web/TUI watcher of a cassette replay sees streaming behaviour unfold in
// real-ish time instead of instantly. These tests pin the contract WITHOUT a real
// LLM and WITHOUT real wall-clock sleeps: pacing is driven by a recording fake
// sleeper injected via withReplaySleeper, and the StreamSink is a recording fake.
//
// What they assert:
//   - OFF (default): zero sleeps, no sink emits, sidecar still written.
//   - ON: every classifiable event reaches the sink IN ORDER, and the sleeper was
//     called with the correct scaled per-event deltas.
//   - synthetic "_kitsoki" rows never reach the sink but still consume their
//     timing slot (their delta is taken).
//   - ctx cancel mid-replay stops emission promptly.

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// recordingSink captures StreamEvents in order for assertion.
type recordingSink struct {
	events []StreamEvent
}

func (s *recordingSink) OnStreamEvent(_ context.Context, ev StreamEvent) {
	s.events = append(s.events, ev)
}

// recordingSleeper records every requested delay and (optionally) cancels a
// supplied context once a trigger count of sleeps is reached, to drive the
// mid-replay-cancel case deterministically.
type recordingSleeper struct {
	delays      []time.Duration
	cancelAfter int                // 0 = never cancel
	cancel      context.CancelFunc // invoked once cancelAfter sleeps have happened
}

func (s *recordingSleeper) sleep(ctx context.Context, d time.Duration) error {
	s.delays = append(s.delays, d)
	if s.cancelAfter > 0 && len(s.delays) >= s.cancelAfter && s.cancel != nil {
		s.cancel()
	}
	return ctx.Err()
}

// assistantEvent builds a minimal claude stream-json assistant event carrying one
// text block, mirroring a recorded transcript row.
func assistantEvent(text string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	})
	return b
}

func syntheticEvent(marker string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"_kitsoki": marker})
	return b
}

func TestSlowPlay_Disabled_NoSleepsNoEmitsButWritesSidecar(t *testing.T) {
	t.Setenv(slowPlayEnv, "") // explicitly disabled (the default)

	sink := &recordingSink{}
	sleeper := &recordingSleeper{}
	dir := t.TempDir()
	w := NewFileTranscriptWriter(dir)

	ctx := WithStreamSink(context.Background(), sink)
	ctx = WithTranscriptWriter(ctx, w)
	ctx = withReplaySleeper(ctx, sleeper.sleep)

	events := []json.RawMessage{assistantEvent("one"), assistantEvent("two")}
	timings := []int64{0, 200}

	ref := WriteReplayTranscript(ctx, "call1", claudeTranscriptFormat, events, timings)

	if len(sleeper.delays) != 0 {
		t.Fatalf("slow-play OFF: want 0 sleeps, got %d (%v)", len(sleeper.delays), sleeper.delays)
	}
	if len(sink.events) != 0 {
		t.Fatalf("slow-play OFF: want 0 sink emits, got %d", len(sink.events))
	}
	if ref == nil || ref.Events != 2 {
		t.Fatalf("sidecar must still be written: ref=%+v", ref)
	}
}

func TestSlowPlay_Enabled_EmitsInOrderWithScaledDeltas(t *testing.T) {
	t.Setenv(slowPlayEnv, "2") // 2× slower than recorded

	sink := &recordingSink{}
	sleeper := &recordingSleeper{}
	dir := t.TempDir()
	w := NewFileTranscriptWriter(dir)

	ctx := WithStreamSink(context.Background(), sink)
	ctx = WithTranscriptWriter(ctx, w)
	ctx = withReplaySleeper(ctx, sleeper.sleep)

	events := []json.RawMessage{
		assistantEvent("alpha"),
		assistantEvent("beta"),
		assistantEvent("gamma"),
	}
	timings := []int64{0, 180, 320} // deltas: 180, 140 ms

	ref := WriteReplayTranscript(ctx, "call2", claudeTranscriptFormat, events, timings)

	// Three classifiable events → three sink emits, in order.
	if len(sink.events) != 3 {
		t.Fatalf("want 3 sink emits, got %d", len(sink.events))
	}
	wantText := []string{"alpha", "beta", "gamma"}
	for i, want := range wantText {
		if sink.events[i].Text != want {
			t.Fatalf("emit %d: want Text %q, got %q", i, want, sink.events[i].Text)
		}
	}

	// One sleep per inter-event gap (between event 0→1 and 1→2), scaled ×2.
	want := []time.Duration{180 * 2 * time.Millisecond, 140 * 2 * time.Millisecond}
	if len(sleeper.delays) != len(want) {
		t.Fatalf("want %d sleeps, got %d (%v)", len(want), len(sleeper.delays), sleeper.delays)
	}
	for i := range want {
		if sleeper.delays[i] != want[i] {
			t.Fatalf("sleep %d: want %v, got %v", i, want[i], sleeper.delays[i])
		}
	}
	if ref == nil || ref.Events != 3 {
		t.Fatalf("sidecar must be written: ref=%+v", ref)
	}
}

func TestSlowPlay_SyntheticRowSkippedButConsumesTimingSlot(t *testing.T) {
	t.Setenv(slowPlayEnv, "1") // real recorded pace

	sink := &recordingSink{}
	sleeper := &recordingSleeper{}
	ctx := WithStreamSink(context.Background(), sink)
	ctx = WithTranscriptWriter(ctx, WithReplaySleeperWriter(t))
	ctx = withReplaySleeper(ctx, sleeper.sleep)

	events := []json.RawMessage{
		assistantEvent("first"),
		syntheticEvent("validator_reject"), // must NOT reach the sink
		assistantEvent("last"),
	}
	timings := []int64{0, 100, 250} // deltas: 100, 150 ms

	WriteReplayTranscript(ctx, "call3", claudeTranscriptFormat, events, timings)

	// Only the two real assistant events reach the sink, in order.
	if len(sink.events) != 2 {
		t.Fatalf("want 2 sink emits (synthetic skipped), got %d", len(sink.events))
	}
	if sink.events[0].Text != "first" || sink.events[1].Text != "last" {
		t.Fatalf("unexpected sink texts: %q, %q", sink.events[0].Text, sink.events[1].Text)
	}

	// But the synthetic row STILL consumed its timing slot: both inter-event
	// gaps were slept (100ms then 150ms), so the surrounding cadence is intact.
	want := []time.Duration{100 * time.Millisecond, 150 * time.Millisecond}
	if len(sleeper.delays) != len(want) {
		t.Fatalf("want %d sleeps, got %d (%v)", len(want), len(sleeper.delays), sleeper.delays)
	}
	for i := range want {
		if sleeper.delays[i] != want[i] {
			t.Fatalf("sleep %d: want %v, got %v", i, want[i], sleeper.delays[i])
		}
	}
}

func TestSlowPlay_CtxCancelMidReplayStopsEmission(t *testing.T) {
	t.Setenv(slowPlayEnv, "1")

	sink := &recordingSink{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel as soon as the first inter-event sleep is requested, so emission
	// should stop before the later events are streamed.
	sleeper := &recordingSleeper{cancelAfter: 1, cancel: cancel}

	ctx = WithStreamSink(ctx, sink)
	ctx = WithTranscriptWriter(ctx, WithReplaySleeperWriter(t))
	ctx = withReplaySleeper(ctx, sleeper.sleep)

	events := []json.RawMessage{
		assistantEvent("e0"),
		assistantEvent("e1"),
		assistantEvent("e2"),
		assistantEvent("e3"),
	}
	timings := []int64{0, 50, 100, 150}

	WriteReplayTranscript(ctx, "call4", claudeTranscriptFormat, events, timings)

	// e0 emits, then the first sleep cancels ctx, so e1..e3 are not emitted.
	if len(sink.events) != 1 {
		t.Fatalf("ctx cancel mid-replay: want 1 emit before stop, got %d", len(sink.events))
	}
	if sink.events[0].Text != "e0" {
		t.Fatalf("want first emit e0, got %q", sink.events[0].Text)
	}
}

// WithReplaySleeperWriter is a tiny helper returning a throwaway file writer into
// a temp dir, so tests that don't assert on sidecar bytes stay terse.
func WithReplaySleeperWriter(t *testing.T) TranscriptWriter {
	t.Helper()
	return NewFileTranscriptWriter(t.TempDir())
}
