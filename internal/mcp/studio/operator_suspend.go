package studio

// operator_suspend.go — the session.answer suspend/resume fallback (slice 8).
//
// For an MCP client that does NOT advertise the elicitation capability, the
// studio cannot push a question down the connection mid-turn. Instead the drive
// turn runs on a background goroutine and parks when the dispatched sub-agent's
// mcp__operator__ask question reaches the studio prompter: session.drive returns
// {awaiting_operator, question_id, questions} immediately, the turn goroutine
// stays alive (the sub-agent still blocked on the per-call socket, the writer
// lock still held — identical to how operator-ask parks the turn today), and the
// client resumes it with session.answer {handle, question_id, answers}.
//
// The broker is the rendezvous between the parked turn goroutine (inside the
// prompter's Ask) and the drive/answer tool calls:
//
//	drive goroutine:  rt.drive(ctx) ──▶ … ──▶ suspendTransport.ask(questions)
//	                                              ├─ publish {question_id, questions}
//	                                              └─ block on answerCh
//	drive handler:    waits for (turnDone | question published) ──▶ awaiting_operator
//	answer handler:   broker.answer(question_id, answers)  (unblocks Ask)
//	                  waits for (turnDone | next question)  ──▶ outcome | awaiting_operator
//
// Bounded wait & teardown: the parked Ask honours the host-supplied ctx (already
// wrapped with the operator-ask timeout), so a client that never answers lets the
// turn fall through to the headless tool-error path; the broker is one-per-turn
// and is discarded when the turn goroutine returns.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"kitsoki/internal/host"
	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui"
)

// pendingQuestion is one parked operator-ask: the forwarded batch plus the
// channel the prompter's Ask is blocked on, waiting for session.answer.
type pendingQuestion struct {
	id        string
	questions []host.OperatorQuestion
	createdAt time.Time
	answerCh  chan map[string]any // buffered (cap 1); answer() sends, ask() drains
}

// turnResult carries the completed turn back to whichever handler is waiting
// (drive when the turn never parked, otherwise answer).
type turnResult struct {
	outcome *orchestrator.TurnOutcome
	frame   tui.Frame
	err     error
}

// suspendBroker rendezvouses a parked turn goroutine with the drive/answer tool
// calls. One broker backs one in-flight suspendable turn on a handle. It is safe
// for concurrent use: the turn goroutine publishes questions / the final result;
// the tool handlers consume them.
type suspendBroker struct {
	mu sync.Mutex

	// questionCh delivers each newly-parked question to the waiting handler.
	// Buffered (cap 1) so the turn goroutine's publish never blocks the dispatch.
	questionCh chan *pendingQuestion
	// doneCh delivers the final turn result exactly once when the turn goroutine
	// returns. Buffered (cap 1).
	doneCh chan turnResult

	pending  map[string]*pendingQuestion
	seq      int
	finished bool
}

func newSuspendBroker() *suspendBroker {
	return &suspendBroker{
		questionCh: make(chan *pendingQuestion, 1),
		doneCh:     make(chan turnResult, 1),
		pending:    map[string]*pendingQuestion{},
	}
}

// park publishes a forwarded question batch and returns the channel the caller
// (the prompter's Ask) blocks on for the operator's answer. Called on the turn
// goroutine.
func (b *suspendBroker) park(questions []host.OperatorQuestion) *pendingQuestion {
	b.mu.Lock()
	b.seq++
	pq := &pendingQuestion{
		id:        fmt.Sprintf("oq-%d", b.seq),
		questions: questions,
		createdAt: time.Now(),
		answerCh:  make(chan map[string]any, 1),
	}
	b.pending[pq.id] = pq
	b.mu.Unlock()
	b.questionCh <- pq
	return pq
}

// answer delivers the operator's answer to a parked question, unblocking the
// turn goroutine. Returns false when the id is unknown (already answered, or the
// turn never parked it).
func (b *suspendBroker) answer(id string, answers map[string]any) bool {
	b.mu.Lock()
	pq, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
	}
	b.mu.Unlock()
	if !ok {
		return false
	}
	pq.answerCh <- answers
	return true
}

// finish records the final turn result exactly once. Called on the turn
// goroutine when rt.drive returns.
func (b *suspendBroker) finish(res turnResult) {
	b.mu.Lock()
	if b.finished {
		b.mu.Unlock()
		return
	}
	b.finished = true
	b.pending = map[string]*pendingQuestion{}
	b.mu.Unlock()
	b.doneCh <- res
}

func (b *suspendBroker) snapshotPending() []pendingQuestion {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.finished {
		return nil
	}
	out := make([]pendingQuestion, 0, len(b.pending))
	for _, p := range b.pending {
		out = append(out, pendingQuestion{
			id:        p.id,
			questions: append([]host.OperatorQuestion(nil), p.questions...),
			createdAt: p.createdAt,
		})
	}
	return out
}

// waitNext blocks until either the turn completes or the next question parks,
// honouring ctx. It is the single rendezvous both the drive and answer handlers
// use after kicking / resuming the turn.
func (b *suspendBroker) waitNext(ctx context.Context) (res turnResult, pq *pendingQuestion, err error) {
	select {
	case r := <-b.doneCh:
		return r, nil, nil
	case q := <-b.questionCh:
		return turnResult{}, q, nil
	case <-ctx.Done():
		return turnResult{}, nil, ctx.Err()
	}
}

// suspendTransport is the operatorAskTransport that parks on a broker. Ask
// publishes the question and blocks until session.answer delivers an answer or
// ctx (carrying the operator-ask timeout) is cancelled — degrading to the
// headless tool-error path on timeout.
type suspendTransport struct {
	broker *suspendBroker
}

func (t *suspendTransport) ask(ctx context.Context, _ string, questions []host.OperatorQuestion) (map[string]any, error) {
	pq := t.broker.park(questions)
	select {
	case answers := <-pq.answerCh:
		return answers, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// awaitingOperator projects a parked question into the awaiting_operator wire
// shape every session.drive / session.answer result carries when the turn paused
// on an operator-ask.
func awaitingOperator(pq *pendingQuestion) AwaitingOperator {
	return AwaitingOperator{
		QuestionID: pq.id,
		Questions:  hostToWireOperatorQuestions(pq.questions),
	}
}

// hostToWireOperatorQuestions maps the host-facing question type to the
// json-tagged mcp wire type carried to the client (the same OperatorAskQuestion
// shape the web prompter and the mcp__operator__ask tool use).
func hostToWireOperatorQuestions(in []host.OperatorQuestion) []kitsokimcp.OperatorAskQuestion {
	out := make([]kitsokimcp.OperatorAskQuestion, 0, len(in))
	for _, q := range in {
		opts := make([]kitsokimcp.OperatorAskOption, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, kitsokimcp.OperatorAskOption{Label: o.Label, Description: o.Description})
		}
		out = append(out, kitsokimcp.OperatorAskQuestion{
			Question:    q.Question,
			Header:      q.Header,
			Options:     opts,
			MultiSelect: q.MultiSelect,
		})
	}
	return out
}
