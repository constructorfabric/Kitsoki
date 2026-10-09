package server_test

// meta_stream_think_test.go — the /rpc/meta-stream frame protocol: extended-
// thinking prose must arrive as its own "think" frame type (never folded into
// a narration "delta"), because the client renders thinking immediately while
// DEFERRING narration (the final reply also arrives as narration). A regression
// here silently re-merges the two and the meta overlay either duplicates the
// reply as a trailing thought or drops thinking from the feed.

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	"kitsoki/internal/runstatus/server"
)

// streamingMetaDriver is a MetaDriver whose Send emits a scripted StreamEvent
// sequence through the ctx StreamSink (the handler installs one) before
// returning the reply — the same shape a real agent turn produces.
type streamingMetaDriver struct {
	fakeMetaDriver
	events []host.StreamEvent
	reply  string
}

func (d *streamingMetaDriver) Send(ctx context.Context, _, _, _ string) (server.MetaSendResult, error) {
	if sink := host.StreamSinkFrom(ctx); sink != nil {
		for _, ev := range d.events {
			sink.OnStreamEvent(ctx, ev)
		}
	}
	return server.MetaSendResult{Assistant: d.reply, ChatID: "chat-1"}, nil
}

// metaStreamFrame mirrors the unexported server-side type for test decoding.
type metaStreamFrame struct {
	Type      string `json:"type"`
	Text      string `json:"text"`
	Tool      string `json:"tool"`
	Preview   string `json:"preview"`
	Assistant string `json:"assistant"`
	Message   string `json:"message"`
}

func readMetaStreamFrames(t *testing.T, ts *httptest.Server, body map[string]any) []metaStreamFrame {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)

	resp, err := http.Post(ts.URL+"/rpc/meta-stream", "application/json", strings.NewReader(string(b)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "expected 200 on meta-stream")
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	var frames []metaStreamFrame
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := strings.TrimPrefix(line, "data: ")
		var f metaStreamFrame
		require.NoError(t, json.Unmarshal([]byte(raw), &f), "unmarshal frame: %s", raw)
		frames = append(frames, f)
	}
	return frames
}

// TestMetaStream_ThinkVsDeltaFrames asserts the full frame split for one
// realistic turn: a thinking-only event, a thinking+narration+tool event
// (think before delta before tool, within the event), a narration-only event
// (the reply), then done.
func TestMetaStream_ThinkVsDeltaFrames(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	driver := &streamingMetaDriver{
		events: []host.StreamEvent{
			{Type: "assistant", Thinking: "Let me check the story."},
			{Type: "assistant", Thinking: "The rooms hold the answer.", Text: "Scanning rooms now.", Tool: "Read", Preview: "rooms/idle.yaml"},
			{Type: "assistant", Text: "the final reply"},
		},
		reply: "the final reply",
	}
	p.putMeta("s1", driver)
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	frames := readMetaStreamFrames(t, ts, map[string]any{
		"session_id": "s1", "mode": "story.ask", "chat_id": "chat-1", "input": "hi",
	})

	type tf struct{ typ, text string }
	var got []tf
	for _, f := range frames {
		switch f.Type {
		case "think", "delta":
			got = append(got, tf{f.Type, f.Text})
		case "tool":
			got = append(got, tf{"tool", f.Tool + ":" + f.Preview})
		}
	}
	assert.Equal(t, []tf{
		{"think", "Let me check the story."},
		{"think", "The rooms hold the answer."},
		{"delta", "Scanning rooms now."},
		{"tool", "Read:rooms/idle.yaml"},
		{"delta", "the final reply"},
	}, got, "thinking must ride 'think' frames, narration 'delta', in event order")

	last := frames[len(frames)-1]
	require.Equal(t, "done", last.Type, "stream must end with done")
	assert.Equal(t, "the final reply", last.Assistant)
}

func TestMetaStream_BackendNeutralActivityFrames(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	driver := &streamingMetaDriver{
		events: []host.StreamEvent{
			{Type: "assistant.message", Text: "I will run the command first."},
			{Type: "tool.execution_start", Tool: "shell", Preview: "echo hi", Tools: []host.StreamToolUse{{Name: "shell", Preview: "echo hi"}}},
			{Type: "assistant.reasoning", Thinking: "The probe output is enough to answer."},
			{Type: "assistant.message", Text: "final reply"},
		},
		reply: "final reply",
	}
	p.putMeta("s1", driver)
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	frames := readMetaStreamFrames(t, ts, map[string]any{
		"session_id": "s1", "mode": "story.ask", "chat_id": "chat-1", "input": "hi",
	})

	type tf struct{ typ, text string }
	var got []tf
	for _, f := range frames {
		switch f.Type {
		case "think", "delta":
			got = append(got, tf{f.Type, f.Text})
		case "tool":
			got = append(got, tf{"tool", f.Tool + ":" + f.Preview})
		}
	}
	assert.Equal(t, []tf{
		{"delta", "I will run the command first."},
		{"tool", "shell:echo hi"},
		{"think", "The probe output is enough to answer."},
		{"delta", "final reply"},
	}, got, "backend-neutral assistant activity must reach the web stream")
}
