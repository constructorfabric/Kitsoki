// operator_questions.go — the web surface's OperatorPrompter.
//
// When a dispatched agent agent forwards a question into kitsoki (see
// internal/host/operator_ask_bridge.go), the in-context OperatorPrompter is the
// thing that surfaces it to the operator and blocks for the answer. On the web
// surface that prompter is webOperatorPrompter:
//
//	Ask(questions)
//	  ├─ register a pending answer channel (questionRegistry)
//	  ├─ append a question frame to the SSE ring (questionBuffer) → browser modal
//	  └─ block until runstatus.session.answer_question resolves the channel
//	       (or the turn's ctx is cancelled / times out)
//
// The SSE side mirrors notifications.go exactly: a server-level ring with
// per-subscription watermarks, streamed over GET /rpc/questions. The only new
// piece is the pending-answer registry, which lets the answer RPC unblock the
// goroutine the agent turn is parked on.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"kitsoki/internal/host"
	kitsokimcp "kitsoki/internal/mcp"
)

// withOperatorPrompter installs a webOperatorPrompter on ctx so the agent
// handlers running inside the upcoming turn can forward questions to this
// browser session. Keyed on the public session_id the SPA routes on; when
// absent (shouldn't happen for a drive turn) ctx is returned unchanged and the
// tool-denied posture applies.
func (s *Server) withOperatorPrompter(ctx context.Context, params map[string]any) context.Context {
	sid, _ := params["session_id"].(string)
	if sid == "" {
		return ctx
	}
	return host.WithOperatorPrompter(ctx, &webOperatorPrompter{
		sessionID: sid,
		buf:       s.questions,
		reg:       s.qreg,
	})
}

// handleQuestions streams the per-session forwarded-question feed as SSE,
// mirroring handleNotifications. The subscription_id comes from
// runstatus.questions.subscribe.
func (s *Server) handleQuestions(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("subscription_id")
	sub := s.questions.lookup(id)
	if sub == nil {
		http.Error(w, "unknown subscription", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher.Flush()

	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		s.streamQuestions(w, flusher, sub)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// streamQuestions emits a runstatus.question frame for every buffered question
// appended since the subscription's last delivery, then advances the watermark.
func (s *Server) streamQuestions(w http.ResponseWriter, flusher http.Flusher, sub *questionSub) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	frames, watermark := s.questions.since(sub.sent)
	for _, fr := range frames {
		frame := map[string]any{
			"jsonrpc": "2.0",
			"method":  "runstatus.question",
			"params": map[string]any{
				"session_id":  fr.SessionID,
				"question_id": fr.QuestionID,
				"questions":   fr.Questions,
			},
		}
		b, err := json.Marshal(frame)
		if err != nil {
			continue
		}
		_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
	}
	sub.sent = watermark
	flusher.Flush()
}

// questionFrame is one entry in the per-session question feed — the wire shape
// carried in the params of a runstatus.question SSE frame. Questions use the
// json-tagged mcp wire type so the browser payload matches the mcp__operator__ask
// tool schema verbatim.
type questionFrame struct {
	SessionID  string                           `json:"session_id"`
	QuestionID string                           `json:"question_id"`
	Questions  []kitsokimcp.OperatorAskQuestion `json:"questions"`
}

// questionBufferCap bounds the in-memory ring (mirrors notifBufferCap).
const questionBufferCap = 256

// questionBuffer is the server-level ring of question frames plus subscription
// watermarks, mirroring notifBuffer. Safe for concurrent use.
type questionBuffer struct {
	mu      sync.Mutex
	frames  []questionFrame
	dropped int
	subs    map[string]*questionSub
	nextID  int
}

type questionSub struct {
	id   string
	mu   sync.Mutex
	sent int
}

func newQuestionBuffer() *questionBuffer {
	return &questionBuffer{subs: map[string]*questionSub{}}
}

func (b *questionBuffer) append(f questionFrame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frames = append(b.frames, f)
	if len(b.frames) > questionBufferCap {
		drop := len(b.frames) - questionBufferCap
		b.frames = b.frames[drop:]
		b.dropped += drop
	}
}

func (b *questionBuffer) subscribe() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	id := "q-sub-" + strconv.Itoa(b.nextID)
	b.subs[id] = &questionSub{id: id, sent: b.dropped + len(b.frames)}
	return id
}

func (b *questionBuffer) unsubscribe(id string) {
	b.mu.Lock()
	delete(b.subs, id)
	b.mu.Unlock()
}

func (b *questionBuffer) lookup(id string) *questionSub {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.subs[id]
}

func (b *questionBuffer) since(sent int) (frames []questionFrame, newWatermark int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	head := b.dropped + len(b.frames)
	if sent < b.dropped {
		sent = b.dropped
	}
	if sent >= head {
		return nil, head
	}
	tail := b.frames[sent-b.dropped:]
	out := make([]questionFrame, len(tail))
	copy(out, tail)
	return out, head
}

// questionRegistry tracks in-flight forwarded questions: question_id → the
// channel the parked agent goroutine is waiting on. answer() resolves one;
// register()/cancel() bracket the wait.
type questionRegistry struct {
	mu      sync.Mutex
	pending map[string]pendingQuestion
	seq     int
}

type pendingQuestion struct {
	ID        string
	SessionID string
	Questions []kitsokimcp.OperatorAskQuestion
	CreatedAt time.Time
	ch        chan map[string]any
}

func newQuestionRegistry() *questionRegistry {
	return &questionRegistry{pending: map[string]pendingQuestion{}}
}

// register allocates a question_id and a buffered (cap-1, so answer() never
// blocks) answer channel.
func (r *questionRegistry) register(sessionID string, questions []kitsokimcp.OperatorAskQuestion) (id string, ch chan map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	id = "q-" + strconv.Itoa(r.seq)
	ch = make(chan map[string]any, 1)
	r.pending[id] = pendingQuestion{
		ID:        id,
		SessionID: sessionID,
		Questions: append([]kitsokimcp.OperatorAskQuestion(nil), questions...),
		CreatedAt: time.Now(),
		ch:        ch,
	}
	return id, ch
}

// answer delivers the operator's answer to a waiting question and removes it.
// Returns false if the id is unknown or already answered/cancelled.
func (r *questionRegistry) answer(id string, answers map[string]any) bool {
	r.mu.Lock()
	p, ok := r.pending[id]
	if ok {
		delete(r.pending, id)
	}
	r.mu.Unlock()
	if !ok {
		return false
	}
	p.ch <- answers
	return true
}

// cancel drops a pending question without delivering an answer (the waiter gave
// up — ctx cancelled / timed out). Idempotent.
func (r *questionRegistry) cancel(id string) {
	r.mu.Lock()
	delete(r.pending, id)
	r.mu.Unlock()
}

func (r *questionRegistry) snapshot() []pendingQuestion {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]pendingQuestion, 0, len(r.pending))
	for _, p := range r.pending {
		p.Questions = append([]kitsokimcp.OperatorAskQuestion(nil), p.Questions...)
		p.ch = nil
		out = append(out, p)
	}
	return out
}

// webOperatorPrompter is the host.OperatorPrompter for the web surface. One is
// installed per turn (bound to the browser-facing session id) at the
// turn/submit/continue RPC sites.
type webOperatorPrompter struct {
	sessionID string // browser-facing (public) session id the SPA routes on
	buf       *questionBuffer
	reg       *questionRegistry
}

var _ host.OperatorPrompter = (*webOperatorPrompter)(nil)

// Ask surfaces the questions on the browser and blocks until the operator
// answers via runstatus.session.answer_question or ctx is done. The sessionID
// argument from the host layer is the orchestrator's internal id; we tag frames
// with the public id the prompter was constructed with (the SPA only resolves
// that one — see the same concern in notificationRelay).
func (p *webOperatorPrompter) Ask(ctx context.Context, _ string, questions []host.OperatorQuestion) (map[string]any, error) {
	wireQuestions := hostToWireQuestions(questions)
	id, ch := p.reg.register(p.sessionID, wireQuestions)
	defer p.reg.cancel(id)

	p.buf.append(questionFrame{
		SessionID:  p.sessionID,
		QuestionID: id,
		Questions:  wireQuestions,
	})

	select {
	case answers := <-ch:
		return answers, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// hostToWireQuestions maps the host-facing question type to the json-tagged mcp
// wire type carried to the browser.
func hostToWireQuestions(in []host.OperatorQuestion) []kitsokimcp.OperatorAskQuestion {
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
