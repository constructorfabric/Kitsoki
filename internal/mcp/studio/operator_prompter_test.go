package studio

// operator_prompter_test.go — slice-8 verification (no LLM, no live client).
//
// The driving MCP client is made the OPERATOR: a studio-driven turn's forwarded
// mcp__operator__ask question is answered by the connecting client. These tests
// pin the proposal's teeth with ZERO LLM and ZERO real claude -p sub-agent:
//
//   2.1 scripted-stub transport: studioOperatorPrompter returns scripted answers
//   2.1 elicitation: a real in-process client answers via its ElicitationHandler,
//       and the operator-probe host binds the chosen label into the story world
//   2.2 fallback protocol: session.drive → awaiting_operator → session.answer →
//       outcome, deterministic (no elicitation capability advertised)
//   2.3 timeout/degrade: no answer within the bound → the probe proceeds without
//       the input (headless behaviour), the turn still settles
//   2.4 trace: routing the studio prompter through the real host listener lands
//       operator.question.asked/answered exactly as for the TUI/web surfaces
//
// The operator-ask round-trip is exercised through a TEST host capability
// (host.operator_probe.ask) registered via the registerExtraHostCaps seam, which
// forwards to the in-context host.OperatorPrompter — the same seam the TUI/web
// run loops install. A replay recording routes the free-text drive to the `ask`
// intent, so no LLM is ever touched.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	kitsokimcp "kitsoki/internal/mcp"
)

const (
	probeApp      = "testdata/operator_probe/app.yaml"
	probeCassette = "testdata/operator_probe/recording.yaml"
	probeQuestion = "Which backend?"
)

// ── transport seam: scripted stub ─────────────────────────────────────────────

// scriptedTransport is the no-LLM operatorAskTransport: it records the forwarded
// questions and returns a canned answer map (or error), the exact "stub prompter
// with scripted answers" pattern the proposal names. It satisfies the same seam
// the elicit/suspend transports do.
type scriptedTransport struct {
	answers map[string]any
	err     error
	gotQs   []host.OperatorQuestion
}

func (s *scriptedTransport) ask(_ context.Context, _ string, qs []host.OperatorQuestion) (map[string]any, error) {
	s.gotQs = qs
	return s.answers, s.err
}

// TestStudioPrompter_ScriptedStub proves studioOperatorPrompter is a thin adapter
// over its transport: the scripted answers come straight back, and a transport
// error propagates (the operator-cancelled / timed-out signal).
func TestStudioPrompter_ScriptedStub(t *testing.T) {
	stub := &scriptedTransport{answers: map[string]any{probeQuestion: "Postgres"}}
	p := newStudioOperatorPrompter(stub)

	qs := []host.OperatorQuestion{{
		Question: probeQuestion, Header: "Backend",
		Options: []host.OperatorOption{{Label: "Postgres"}, {Label: "SQLite"}},
	}}
	answers, err := p.Ask(context.Background(), "sess-1", qs)
	require.NoError(t, err)
	assert.Equal(t, "Postgres", answers[probeQuestion])
	assert.Equal(t, qs, stub.gotQs, "the transport saw the forwarded questions")

	// A nil transport degrades to an error (the headless tool-error path).
	_, err = newStudioOperatorPrompter(nil).Ask(context.Background(), "s", qs)
	require.Error(t, err)
}

// ── operator-probe host (forwards to the in-context prompter) ──────────────────

// operatorProbeHost is the TEST host capability host.operator_probe.ask: it
// forwards a fixed multiple-choice question to the in-context OperatorPrompter
// and binds the chosen label into world.answer. timeout bounds the wait exactly
// as the real operator-ask bridge does (handleConn wraps ctx with the operator-
// ask timeout); on any error it DEGRADES — proceeding without the input — so a
// non-responding client falls back to headless behaviour rather than hanging.
func operatorProbeHost(timeout time.Duration) host.Handler {
	return func(ctx context.Context, _ map[string]any) (host.Result, error) {
		prompter, ok := host.OperatorPrompterFrom(ctx)
		if !ok {
			return host.Result{Data: map[string]any{"answer": "(no operator attached)"}}, nil
		}
		qs := []host.OperatorQuestion{{
			Question: probeQuestion,
			Header:   "Backend",
			Options: []host.OperatorOption{
				{Label: "Postgres", Description: "managed pg"},
				{Label: "SQLite", Description: "embedded"},
			},
		}}
		askCtx := ctx
		if timeout > 0 {
			var cancel context.CancelFunc
			askCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		answers, err := prompter.Ask(askCtx, host.KitsokiSessionIDFromCtx(ctx), qs)
		if err != nil {
			// Graceful degrade: the operator did not answer; proceed without it.
			return host.Result{Data: map[string]any{"answer": "(degraded: no operator answer)"}}, nil
		}
		label, _ := answers[probeQuestion].(string)
		return host.Result{Data: map[string]any{"answer": label}}, nil
	}
}

// withProbeHost installs the operator-probe host for the duration of one test,
// restoring the seam after. Driving runtimes built while it is set carry the
// probe; production (nil seam) never does.
func withProbeHost(t *testing.T, timeout time.Duration) {
	t.Helper()
	prev := registerExtraHostCaps
	registerExtraHostCaps = func(reg *host.Registry) {
		reg.Register("host.operator_probe.ask", operatorProbeHost(timeout))
	}
	t.Cleanup(func() { registerExtraHostCaps = prev })
}

// openProbe opens a replay-backed operator_probe driving handle on srv's session.
func openProbe(t *testing.T, srv *Server) *SessionHandle {
	t.Helper()
	sh, err := srv.sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		Mode:          HarnessReplay,
		RecordingPath: probeCassette,
		StoryPath:     probeApp,
		TracePath:     filepath.Join(t.TempDir(), "trace.jsonl"),
	})
	require.NoError(t, err)
	return sh
}

func probeServer(t *testing.T) *Server {
	t.Helper()
	return NewServer(NewStudioSession(func(mode HarnessMode, rec, _ string) (harness.Harness, error) {
		return harness.NewReplay(rec)
	}))
}

type blockingHarness struct {
	cancelled chan struct{}
	once      sync.Once
}

func (h *blockingHarness) RunTurn(ctx context.Context, _ harness.TurnInput) (mcpsdk.CallToolParams, error) {
	<-ctx.Done()
	h.once.Do(func() { close(h.cancelled) })
	return mcpsdk.CallToolParams{}, ctx.Err()
}

func (h *blockingHarness) Close() error { return nil }

func TestStudioOperatorAsk_FallbackCancelsPreParkTimeout(t *testing.T) {
	h := &blockingHarness{cancelled: make(chan struct{})}
	srv := NewServer(NewStudioSession(func(mode HarnessMode, _, _ string) (harness.Harness, error) {
		require.Equal(t, HarnessLive, mode)
		return h, nil
	}))
	dir := t.TempDir()
	storyPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(storyPath, []byte(`
app:
  id: cancellation-probe
  version: 0.1.0
intents:
  done:
    title: Done
    examples: ["done"]
root: idle
states:
  idle:
    view: idle
    on:
      done:
        - target: idle
`), 0o644))
	sh, err := srv.sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		Mode:      HarnessLive,
		StoryPath: storyPath,
		TracePath: filepath.Join(t.TempDir(), "trace.jsonl"),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, _, turnDone, running, err := sh.Runtime.driveSuspendable(ctx, "route slowly", 100, 30, 0)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.False(t, turnDone)
	require.Nil(t, running)
	require.Nil(t, sh.Runtime.inFlight, "a pre-park timeout must not leave the handle awaiting an answer")

	select {
	case <-h.cancelled:
	case <-time.After(time.Second):
		t.Fatal("driveSuspendable did not cancel the underlying turn after the MCP call timed out")
	}
}

// ── 2.2 fallback protocol: drive → awaiting_operator → session.answer → outcome ─

// TestStudioOperatorAsk_FallbackProtocol drives the operator_probe story through
// the runtime's suspend/resume fallback (no elicitation capability): the drive
// parks on the operator-ask and returns awaiting_operator; session.answer
// delivers the chosen label and the turn settles with world.answer set — all
// deterministic, no LLM, no live client.
func TestStudioOperatorAsk_FallbackProtocol(t *testing.T) {
	withProbeHost(t, 0)
	srv := probeServer(t)
	sh := openProbe(t, srv)

	// Drive: the turn parks on the operator-ask → awaiting_operator (no settle).
	res, pq, turnDone, running, err := sh.Runtime.driveSuspendable(context.Background(), "ask the operator", 100, 30, 0)
	require.NoError(t, err)
	require.False(t, turnDone, "the turn must park on the operator-ask, not settle")
	require.Nil(t, running)
	require.NotNil(t, pq)
	require.NotEmpty(t, pq.id, "a question id correlates the park with the answer")
	require.Len(t, pq.questions, 1)
	assert.Equal(t, probeQuestion, pq.questions[0].Question)

	// Resume with the operator's choice → the turn settles, world.answer is bound.
	res, pq2, turnDone, ok, err := sh.Runtime.resumeSuspendable(
		context.Background(), pq.id, map[string]any{probeQuestion: "Postgres"})
	require.NoError(t, err)
	require.True(t, ok, "the question id was awaiting an answer")
	require.True(t, turnDone, "the turn settles once answered")
	require.Nil(t, pq2)
	require.NoError(t, res.err, "the turn completed cleanly")

	ins, err := sh.Runtime.inspect(context.Background(), 5, sh.Key)
	require.NoError(t, err)
	assert.Equal(t, "Postgres", ins.World["answer"], "the story branched on the operator's answer")
}

// TestStudioOperatorAsk_FallbackOverMCP runs the same fallback end-to-end over
// the studio MCP surface: session.drive returns the awaiting_operator status and
// session.answer resumes to the settled outcome. This pins the new tools +
// wire status, not just the runtime.
func TestStudioOperatorAsk_FallbackOverMCP(t *testing.T) {
	withProbeHost(t, 0)
	srv := probeServer(t)
	cs := connectClient(t, srv, nil) // no ElicitationHandler → fallback path

	openProbeOverMCP(t, cs)

	// drive → awaiting_operator
	var driveResp TurnResponse
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "session.drive",
		Arguments: map[string]any{"handle": "s1", "input": "ask the operator"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.drive: %s", textOf(res))
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &driveResp))
	require.NotNil(t, driveResp.AwaitingOperator, "drive parks → awaiting_operator")
	qid := driveResp.AwaitingOperator.QuestionID
	require.NotEmpty(t, qid)
	require.Len(t, driveResp.AwaitingOperator.Questions, 1)
	assert.Equal(t, probeQuestion, driveResp.AwaitingOperator.Questions[0].Question)

	var work WorkResult
	res, err = cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "studio.work",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "studio.work: %s", textOf(res))
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &work))
	assert.Equal(t, 1, work.Summary.OperatorQuestions)
	assert.Equal(t, 0, work.Summary.RunningDrive)
	assert.Equal(t, 1, work.Summary.NeedsAttention)
	require.Len(t, work.Items, 1)
	assert.Equal(t, "operator_question", work.Items[0].Kind)
	assert.Equal(t, "awaiting_answer", work.Items[0].Status)
	assert.Equal(t, qid, work.Items[0].QuestionID)
	assert.Equal(t, probeQuestion, work.Items[0].Body)
	require.Len(t, work.Items[0].Questions, 1)
	assert.Equal(t, probeQuestion, work.Items[0].Questions[0].Question)
	assert.Equal(t, "session.answer", work.Items[0].Reacquire.Tool)
	assert.Equal(t, "s1", work.Items[0].Reacquire.Args["handle"])
	assert.Equal(t, qid, work.Items[0].Reacquire.Args["question_id"])

	var status SessionStatusResult
	res, err = cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "session.status",
		Arguments: map[string]any{"handle": "s1"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.status: %s", textOf(res))
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &status))
	require.Nil(t, status.Running, "compact polling must not report a parked operator-ask as running")

	var inspect InspectResult
	res, err = cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "session.inspect",
		Arguments: map[string]any{"handle": "s1"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.inspect: %s", textOf(res))
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &inspect))
	require.Nil(t, inspect.Running, "a parked operator-ask is awaiting_operator, not a generic running drive")
	assert.Equal(t, 0, inspect.Async.RunningDrive)
	assert.Equal(t, 1, inspect.Async.OperatorQuestions)
	require.Len(t, inspect.OperatorQuestions, 1)
	assert.Equal(t, qid, inspect.OperatorQuestions[0].QuestionID)
	require.Len(t, inspect.OperatorQuestions[0].Questions, 1)
	assert.Equal(t, probeQuestion, inspect.OperatorQuestions[0].Questions[0].Question)
	assert.Equal(t, "session.answer", inspect.OperatorQuestions[0].Reacquire.Tool)
	assert.Equal(t, "s1", inspect.OperatorQuestions[0].Reacquire.Args["handle"])
	assert.Equal(t, qid, inspect.OperatorQuestions[0].Reacquire.Args["question_id"])

	// session.answer → settled outcome
	var ansResp TurnResponse
	res, err = cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: work.Items[0].Reacquire.Tool,
		Arguments: map[string]any{
			"handle": work.Items[0].Reacquire.Args["handle"], "question_id": work.Items[0].Reacquire.Args["question_id"],
			"answers": map[string]any{probeQuestion: "SQLite"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.answer: %s", textOf(res))
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &ansResp))
	require.Nil(t, ansResp.AwaitingOperator, "the turn settled, no further question")
	assert.Equal(t, "probe", ansResp.Outcome.State)
	assert.Contains(t, ansResp.Frame.Text, "SQLite", "the view branched on the answer")

	res, err = cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "studio.work",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "studio.work after answer: %s", textOf(res))
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &work))
	assert.Equal(t, 0, work.Summary.OperatorQuestions)
}

// TestStudioOperatorAsk_AnswerUnknownQuestion proves session.answer fails fast
// (a structured error) when no turn is awaiting the named question id.
func TestStudioOperatorAsk_AnswerUnknownQuestion(t *testing.T) {
	withProbeHost(t, 0)
	srv := probeServer(t)
	cs := connectClient(t, srv, nil)
	openProbeOverMCP(t, cs)

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "session.answer",
		Arguments: map[string]any{
			"handle": "s1", "question_id": "oq-999",
			"answers": map[string]any{probeQuestion: "Postgres"},
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "answering a non-pending question is an error")
	assert.Contains(t, textOf(res), "no turn awaiting")
}

// ── 2.1 elicitation: a real in-process client answers via its handler ──────────

// TestStudioOperatorAsk_Elicitation drives the same story against a client that
// advertises elicitation: the forwarded question is mapped to an elicitation
// schema, the client's ElicitationHandler picks an option, and the accepted
// data is mapped back to the AskUserQuestion answer shape and bound into the
// world. session.drive stays one settling call (no awaiting_operator).
func TestStudioOperatorAsk_Elicitation(t *testing.T) {
	withProbeHost(t, 0)
	srv := probeServer(t)

	var gotSchema map[string]any
	elicit := func(_ context.Context, req *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
		// The schema carries the question as a flat top-level property with an
		// enum of the option labels (elicitation forbids nesting).
		if s, ok := req.Params.RequestedSchema.(map[string]any); ok {
			gotSchema = s
		}
		return &mcpsdk.ElicitResult{Action: "accept", Content: map[string]any{"q0": "Postgres"}}, nil
	}
	cs := connectClient(t, srv, &mcpsdk.ClientOptions{ElicitationHandler: elicit})
	openProbeOverMCP(t, cs)

	var driveResp TurnResponse
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "session.drive",
		Arguments: map[string]any{"handle": "s1", "input": "ask the operator"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.drive: %s", textOf(res))
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &driveResp))

	require.Nil(t, driveResp.AwaitingOperator, "elicitation settles in one drive call")
	assert.Equal(t, "probe", driveResp.Outcome.State)
	assert.Contains(t, driveResp.Frame.Text, "Postgres", "the view branched on the elicited answer")
	require.NotNil(t, gotSchema, "the client received an elicitation schema")
	props, _ := gotSchema["properties"].(map[string]any)
	require.Contains(t, props, "q0", "each question is a flat top-level property")
}

// TestStudioPrompter_ElicitDecline proves a declined/cancelled elicitation maps
// to an error (so the host bridge degrades to "proceed without this input").
func TestStudioPrompter_ElicitDecline(t *testing.T) {
	withProbeHost(t, 0)
	srv := probeServer(t)
	elicit := func(_ context.Context, _ *mcpsdk.ElicitRequest) (*mcpsdk.ElicitResult, error) {
		return &mcpsdk.ElicitResult{Action: "decline"}, nil
	}
	cs := connectClient(t, srv, &mcpsdk.ClientOptions{ElicitationHandler: elicit})
	openProbeOverMCP(t, cs)

	var driveResp TurnResponse
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "session.drive",
		Arguments: map[string]any{"handle": "s1", "input": "ask the operator"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.NoError(t, json.Unmarshal([]byte(textOf(res)), &driveResp))
	// The probe host degrades on the elicit error: the turn still settles.
	assert.Equal(t, "probe", driveResp.Outcome.State)
	assert.Contains(t, driveResp.Frame.Text, "degraded", "a declined elicitation degrades gracefully")
}

// ── 2.3 timeout / graceful degrade ─────────────────────────────────────────────

// TestStudioOperatorAsk_TimeoutDegrades proves a never-answered operator-ask
// under the fallback degrades to headless behaviour: the drive parks
// (awaiting_operator), the client never calls session.answer, the probe's
// bounded wait fires, and the parked turn settles on its own — proceeding
// without the input rather than hanging forever.
func TestStudioOperatorAsk_TimeoutDegrades(t *testing.T) {
	withProbeHost(t, 50*time.Millisecond) // tiny bound: the operator never answers
	srv := probeServer(t)
	sh := openProbe(t, srv)

	// Drive parks on the operator-ask → awaiting_operator (the turn is alive).
	res, pq, turnDone, running, err := sh.Runtime.driveSuspendable(context.Background(), "ask the operator", 100, 30, 0)
	require.NoError(t, err)
	require.False(t, turnDone, "the turn parks awaiting the operator")
	require.Nil(t, running)
	require.NotNil(t, pq)
	_ = res

	// Nobody answers. The 50ms bound inside the probe fires, the probe degrades,
	// and the parked turn settles in the background — observable on the broker.
	broker := sh.Runtime.inFlight
	require.NotNil(t, broker, "the broker is live while parked")
	select {
	case done := <-broker.doneCh:
		require.NoError(t, done.err, "the turn settled (degraded), not errored")
	case <-time.After(5 * time.Second):
		t.Fatal("parked turn hung: a timed-out operator-ask must degrade, not block forever")
	}

	ins, err := sh.Runtime.inspect(context.Background(), 5, sh.Key)
	require.NoError(t, err)
	assert.Contains(t, ins.World["answer"], "degraded", "the story proceeded without the operator")
}

// ── 2.4 trace: operator.question.asked/answered land as for TUI/web ────────────

// TestStudioPrompter_TraceViaHostListener routes the studio prompter through the
// REAL host operator-ask listener (the same code the TUI/web prompters run
// behind) and dials the socket exactly as the mcp-operator-ask grandchild does.
// It asserts the operator.question.asked/answered events land with the
// correlatable structured attrs — identical to the TUI/web surfaces, because it
// is the identical listener.
func TestStudioPrompter_TraceViaHostListener(t *testing.T) {
	logBuf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	prompter := newStudioOperatorPrompter(&scriptedTransport{
		answers: map[string]any{probeQuestion: "Postgres"},
	})
	l, err := host.StartOperatorAskListenerInMemoryForTest(context.Background(), prompter, "studio-sess", time.Minute)
	require.NoError(t, err)
	defer l.Close()

	resp := dialOperatorAsk(t, l.Dial, kitsokimcp.OperatorAskRequest{
		Questions: []kitsokimcp.OperatorAskQuestion{{
			Question: probeQuestion, Header: "Backend",
			Options: []kitsokimcp.OperatorAskOption{{Label: "Postgres"}, {Label: "SQLite"}},
		}},
	})
	require.Empty(t, resp.Error)
	assert.Equal(t, "Postgres", resp.Answers[probeQuestion])

	require.Eventually(t, func() bool {
		return bytes.Contains(logBuf.Bytes(), []byte("operator.question.answered"))
	}, time.Second, 10*time.Millisecond)
	logs := logBuf.String()
	assert.Contains(t, logs, "operator.question.asked")
	assert.Contains(t, logs, "session_id=studio-sess")
	assert.Contains(t, logs, "outcome=answered")
	assert.Contains(t, logs, "question_id=")
}

// ── helpers ────────────────────────────────────────────────────────────────────

// connectClient wires an in-process MCP client/server pair, optionally with
// client options (e.g. an ElicitationHandler that makes the client advertise the
// elicitation capability).
func connectClient(t *testing.T, srv *Server, opts *mcpsdk.ClientOptions) *mcpsdk.ClientSession {
	t.Helper()
	t1, t2 := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	_, err := srv.Connect(ctx, t1, nil)
	require.NoError(t, err)
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "v0"}, opts)
	cs, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// openProbeOverMCP opens an operator_probe replay handle via session.new (key s1).
func openProbeOverMCP(t *testing.T, cs *mcpsdk.ClientSession) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name: "session.new",
		Arguments: map[string]any{
			"story_path": probeApp,
			"harness":    "replay",
			"cassette":   probeCassette,
			"trace":      filepath.Join(t.TempDir(), "trace.jsonl"),
			"key":        "s1",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", textOf(res))
}

// textOf returns the first text content block of a tool result.
func textOf(res *mcpsdk.CallToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
		return tc.Text
	}
	return ""
}

// dialOperatorAsk sends one request to the host listener and reads one response,
// mirroring what the mcp-operator-ask grandchild does on the wire.
func dialOperatorAsk(t *testing.T, dial func() (net.Conn, error), req kitsokimcp.OperatorAskRequest) kitsokimcp.OperatorAskResponse {
	t.Helper()
	conn, err := dial()
	require.NoError(t, err)
	defer conn.Close()
	payload, _ := json.Marshal(req)
	_, err = conn.Write(append(payload, '\n'))
	require.NoError(t, err)
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	require.NoError(t, err)
	var resp kitsokimcp.OperatorAskResponse
	require.NoError(t, json.Unmarshal(line, &resp))
	return resp
}
