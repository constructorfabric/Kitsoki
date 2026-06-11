package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
)

func TestQuestionRegistry_AnswerUnblocks(t *testing.T) {
	reg := newQuestionRegistry()
	id, ch := reg.register()
	require.NotEmpty(t, id)

	go reg.answer(id, map[string]any{"q": "yes"})
	select {
	case got := <-ch:
		assert.Equal(t, "yes", got["q"])
	case <-time.After(time.Second):
		t.Fatal("answer did not unblock the channel")
	}
}

func TestQuestionRegistry_AnswerUnknownReturnsFalse(t *testing.T) {
	reg := newQuestionRegistry()
	assert.False(t, reg.answer("nope", map[string]any{}))

	// And a cancelled question can't be answered.
	id, _ := reg.register()
	reg.cancel(id)
	assert.False(t, reg.answer(id, map[string]any{}))
}

func TestQuestionBuffer_SubscribeSeedsAtHeadAndStreamsTail(t *testing.T) {
	b := newQuestionBuffer()
	b.append(questionFrame{QuestionID: "q-old"}) // before subscribe — not delivered
	subID := b.subscribe()
	sub := b.lookup(subID)
	require.NotNil(t, sub)

	frames, wm := b.since(sub.sent)
	assert.Empty(t, frames, "subscribe seeds at head; nothing pre-existing is replayed")

	b.append(questionFrame{QuestionID: "q-new", SessionID: "s1"})
	frames, wm = b.since(sub.sent)
	require.Len(t, frames, 1)
	assert.Equal(t, "q-new", frames[0].QuestionID)
	sub.sent = wm
	frames, _ = b.since(sub.sent)
	assert.Empty(t, frames, "watermark advanced; no re-delivery")
}

func TestWebOperatorPrompter_BlocksThenAnswers(t *testing.T) {
	buf := newQuestionBuffer()
	reg := newQuestionRegistry()
	p := &webOperatorPrompter{sessionID: "pub-1", buf: buf, reg: reg}

	type result struct {
		answers map[string]any
		err     error
	}
	done := make(chan result, 1)
	go func() {
		a, e := p.Ask(context.Background(), "internal-sid", []host.OperatorQuestion{{
			Question: "Ship it?",
			Header:   "Ship",
			Options:  []host.OperatorOption{{Label: "Yes"}, {Label: "No"}},
		}})
		done <- result{a, e}
	}()

	// The question frame must surface on the buffer, tagged with the PUBLIC id.
	var qid string
	require.Eventually(t, func() bool {
		frames, _ := buf.since(0)
		if len(frames) == 1 {
			qid = frames[0].QuestionID
			assert.Equal(t, "pub-1", frames[0].SessionID)
			require.Len(t, frames[0].Questions, 1)
			assert.Equal(t, "Ship", frames[0].Questions[0].Header)
			return true
		}
		return false
	}, time.Second, 5*time.Millisecond)

	// Answering unblocks Ask with the operator's selection.
	require.True(t, reg.answer(qid, map[string]any{"Ship it?": "Yes"}))
	select {
	case r := <-done:
		require.NoError(t, r.err)
		assert.Equal(t, "Yes", r.answers["Ship it?"])
	case <-time.After(time.Second):
		t.Fatal("Ask did not return after answer")
	}
}

func TestWebOperatorPrompter_CtxCancelReturnsError(t *testing.T) {
	p := &webOperatorPrompter{sessionID: "pub", buf: newQuestionBuffer(), reg: newQuestionRegistry()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, e := p.Ask(ctx, "sid", []host.OperatorQuestion{{Question: "q"}})
		done <- e
	}()
	cancel()
	select {
	case e := <-done:
		require.Error(t, e)
	case <-time.After(time.Second):
		t.Fatal("Ask did not return on ctx cancel")
	}
}

// TestAnswerQuestionRPC_UnblocksPrompter drives the full server path: a prompter
// parked on Ask is released by the runstatus.session.answer_question dispatch.
func TestAnswerQuestionRPC_UnblocksPrompter(t *testing.T) {
	s := newServer(nil, newConfig(nil))
	ctx := s.withOperatorPrompter(context.Background(), map[string]any{"session_id": "pub-9"})
	prompter, ok := host.OperatorPrompterFrom(ctx)
	require.True(t, ok)

	done := make(chan map[string]any, 1)
	go func() {
		a, _ := prompter.Ask(ctx, "sid", []host.OperatorQuestion{{Question: "Pick", Options: []host.OperatorOption{{Label: "A"}}}})
		done <- a
	}()

	// Grab the generated question_id off the feed.
	var qid string
	require.Eventually(t, func() bool {
		frames, _ := s.questions.since(0)
		if len(frames) == 1 {
			qid = frames[0].QuestionID
			return true
		}
		return false
	}, time.Second, 5*time.Millisecond)

	// Unknown id is a not-found error.
	_, rerr := s.dispatch(context.Background(), "runstatus.session.answer_question",
		map[string]any{"question_id": "bogus"})
	require.NotNil(t, rerr)

	// Correct id unblocks Ask.
	out, rerr := s.dispatch(context.Background(), "runstatus.session.answer_question",
		map[string]any{"question_id": qid, "answers": map[string]any{"Pick": "A"}})
	require.Nil(t, rerr)
	assert.Equal(t, map[string]any{"ok": true}, out)

	select {
	case a := <-done:
		assert.Equal(t, "A", a["Pick"])
	case <-time.After(time.Second):
		t.Fatal("prompter not unblocked by answer_question RPC")
	}
}

func TestQuestionsSubscribeUnsubscribeRPC(t *testing.T) {
	s := newServer(nil, newConfig(nil))
	out, rerr := s.dispatch(context.Background(), "runstatus.questions.subscribe", map[string]any{})
	require.Nil(t, rerr)
	subID := out.(map[string]any)["subscription_id"].(string)
	require.NotEmpty(t, subID)
	require.NotNil(t, s.questions.lookup(subID))

	_, rerr = s.dispatch(context.Background(), "runstatus.questions.unsubscribe", map[string]any{"subscription_id": subID})
	require.Nil(t, rerr)
	require.Nil(t, s.questions.lookup(subID))
}
