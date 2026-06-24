package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/orchestrator"
)

// runBatch executes a tea.Cmd, flattening any tea.BatchMsg, and returns the
// first non-nil, non-spinner-tick message produced. This lets a test observe
// the async turn's terminal message without standing up a tea.Program.
func runBatch(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if isSpinnerTick(msg) {
			continue
		}
		return msg
	}
	return nil
}

func isSpinnerTick(msg tea.Msg) bool {
	// spinner.TickMsg is the only animation message startAsyncTurn batches.
	_, ok := msg.(spinner.TickMsg)
	return ok
}

// errBoom is a genuine, non-cancellation failure.
var errBoom = errors.New("boom: genuine non-cancellation failure")

// TestStartAsyncTurn_GenuineErrorWhileCtxCancelled reproduces the false-positive
// described in the code review: when the turn ctx has been cancelled
// asynchronously but the work returned a real (non-cancellation) error, the old
// code classified it as a clean cancellation by inspecting ctx.Err() alone.
//
// The fix classifies via errors.Is(err, context.Canceled). This test cancels the
// in-flight ctx (simulating an async cancel) and then runs the cmd with a stub
// that returns errBoom; the resulting message MUST carry the genuine error and
// MUST NOT be a ModeCancelled outcome.
func TestStartAsyncTurn_GenuineErrorWhileCtxCancelled(t *testing.T) {
	t.Parallel()

	m := RootModel{}
	run := func(ctx context.Context) (*orchestrator.TurnOutcome, error) {
		return nil, errBoom
	}
	next, cmd := startAsyncTurn(m, "hello", run, pendingLLM)

	// Simulate an asynchronous cancellation that landed before/while the work
	// produced its genuine error. ctx.Err() is now non-nil.
	if next.inFlightCancel != nil {
		next.inFlightCancel()
	}

	msg := runBatch(t, cmd)
	to, ok := msg.(turnOutcomeMsg)
	if !ok {
		t.Fatalf("expected turnOutcomeMsg, got %T", msg)
	}
	if to.err == nil {
		t.Fatalf("genuine error was swallowed; got nil err (misclassified as cancellation)")
	}
	if !errors.Is(to.err, errBoom) {
		t.Fatalf("expected errBoom to be surfaced, got %v", to.err)
	}
	if to.outcome != nil && to.outcome.Mode == orchestrator.ModeCancelled {
		t.Fatalf("genuine error must NOT be classified as ModeCancelled")
	}
}

// TestOffPathReplyFor_SurfacesGenuineError mirrors the off-path call site. The
// regression guard is behavioral: when AskOffPath returns a genuine
// non-cancellation error, offPathReplyFor must surface that exact error — never
// substitute a synthesized context.Canceled, which is what the old
// `if ctx.Err() != nil { return ctx.Err() }` code would have done had the turn
// ctx been cancelled asynchronously. offPathReplyFor takes no context precisely
// so that misclassification is structurally impossible.
func TestOffPathReplyFor_SurfacesGenuineError(t *testing.T) {
	t.Parallel()

	// Simulate an async cancel that has already fired by the time the reply is
	// built — the historical trigger for the false positive.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if ctx.Err() == nil {
		t.Fatalf("precondition: ctx should be cancelled")
	}

	got := offPathReplyFor("why?", "", errBoom)
	if got.err == nil {
		t.Fatalf("genuine error was swallowed; got nil err")
	}
	if !errors.Is(got.err, errBoom) {
		t.Fatalf("expected errBoom surfaced verbatim, got %v", got.err)
	}
	if errors.Is(got.err, context.Canceled) {
		t.Fatalf("genuine error must NOT be replaced by context.Canceled")
	}
	if got.answer != "" {
		t.Fatalf("error reply must carry no answer, got %q", got.answer)
	}
}

// TestOffPathReplyFor_HappyPath confirms a successful answer round-trips.
func TestOffPathReplyFor_HappyPath(t *testing.T) {
	t.Parallel()

	got := offPathReplyFor("hi", "the answer", nil)
	if got.err != nil {
		t.Fatalf("unexpected err: %v", got.err)
	}
	if got.answer != "the answer" {
		t.Fatalf("expected answer round-trip, got %q", got.answer)
	}
	if got.question != "hi" {
		t.Fatalf("expected question round-trip, got %q", got.question)
	}
}

// TestStartAsyncTurn_RealCancellation confirms the happy classification path
// still holds: a context.Canceled error is reported as ModeCancelled with no
// surfaced error.
func TestStartAsyncTurn_RealCancellation(t *testing.T) {
	t.Parallel()

	m := RootModel{}
	run := func(ctx context.Context) (*orchestrator.TurnOutcome, error) {
		return nil, context.Canceled
	}
	_, cmd := startAsyncTurn(m, "hello", run, pendingLLM)

	msg := runBatch(t, cmd)
	to, ok := msg.(turnOutcomeMsg)
	if !ok {
		t.Fatalf("expected turnOutcomeMsg, got %T", msg)
	}
	if to.err != nil {
		t.Fatalf("cancellation should not surface an error, got %v", to.err)
	}
	if to.outcome == nil || to.outcome.Mode != orchestrator.ModeCancelled {
		t.Fatalf("expected ModeCancelled outcome, got %+v", to.outcome)
	}
}
