package inbound

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/transport"
)

// fakeSource returns a fixed batch of replies on every poll (the "returns the
// whole thread each time" shape), so the bridge's dedup is exercised.
type fakeSource struct {
	key     transport.SessionKey
	replies []Reply
	err     error
}

func (f *fakeSource) Key() transport.SessionKey { return f.key }
func (f *fakeSource) Poll(context.Context) ([]Reply, error) {
	return f.replies, f.err
}

// recordingDriver captures every turn the bridge drives.
type recordingDriver struct {
	turns []driven
	err   error
}

type driven struct {
	intent string
	slots  map[string]any
	author string
}

func (d *recordingDriver) SubmitIntent(_ context.Context, intent string, slots map[string]any, author string) error {
	if d.err != nil {
		return d.err
	}
	d.turns = append(d.turns, driven{intent, slots, author})
	return nil
}

func TestPrefixClassifier(t *testing.T) {
	c := PrefixClassifier{}
	cases := []struct {
		body       string
		wantIntent string
		wantOK     bool
		wantSlot   string // refine_feedback / target value, if any
	}{
		{"continue", "continue", true, ""},
		{"LGTM", "continue", true, ""},
		{"approve please", "continue", true, ""},
		{"refine: tighten the repro", "refine", true, "tighten the repro"},
		{"refine add a test", "refine", true, "add a test"},
		{"restart_from reproducing", "restart_from", true, "reproducing"},
		{"jump_to done", "jump_to", true, "done"},
		{"restart_from", "", false, ""},        // missing target
		{"thanks, looks great", "", false, ""}, // chatter
		{"", "", false, ""},
		{"  \n  continue  \n more", "continue", true, ""}, // leading blank lines
	}
	for _, tc := range cases {
		got, ok := c.Classify(tc.body)
		assert.Equal(t, tc.wantOK, ok, "ok for %q", tc.body)
		if !tc.wantOK {
			continue
		}
		assert.Equal(t, tc.wantIntent, got.Intent, "intent for %q", tc.body)
		if tc.wantSlot != "" {
			// the single slot value (refine_feedback or target)
			var v any
			for _, sv := range got.Slots {
				v = sv
			}
			assert.Equal(t, tc.wantSlot, v, "slot for %q", tc.body)
		}
	}
}

func TestBridge_DrivesClassifiedReplies(t *testing.T) {
	src := &fakeSource{
		key: transport.SessionKey{Transport: "jira", Thread: "PLTFRM-1"},
		replies: []Reply{
			{ID: "1", Author: "alice", Body: "continue"},
			{ID: "2", Author: "bob", Body: "refine: add a regression test"},
		},
	}
	drv := &recordingDriver{}
	b := &Bridge{Source: src, Classifier: PrefixClassifier{}, Driver: drv}

	n, err := b.PollOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	require.Len(t, drv.turns, 2)
	assert.Equal(t, "continue", drv.turns[0].intent)
	assert.Equal(t, "alice", drv.turns[0].author)
	assert.Equal(t, "refine", drv.turns[1].intent)
	assert.Equal(t, "add a regression test", drv.turns[1].slots["refine_feedback"])
	assert.Equal(t, "bob", drv.turns[1].author)
}

func TestBridge_FiltersBotMarkerSelfOutput(t *testing.T) {
	src := &fakeSource{replies: []Reply{
		{ID: "1", Author: "kitsoki-bot", Body: transport.DefaultBotMarker + " *Phase A complete*\n\ncontinue"},
		{ID: "2", Author: "alice", Body: "continue"},
	}}
	drv := &recordingDriver{}
	b := &Bridge{Source: src, Classifier: PrefixClassifier{}, Driver: drv}

	n, err := b.PollOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the bot's own echoed output must not drive a turn")
	require.Len(t, drv.turns, 1)
	assert.Equal(t, "alice", drv.turns[0].author)
}

func TestBridge_AuthorAllowList(t *testing.T) {
	src := &fakeSource{replies: []Reply{
		{ID: "1", Author: "stranger", Body: "continue"},
		{ID: "2", Author: "alice", Body: "continue"},
	}}
	drv := &recordingDriver{}
	b := &Bridge{Source: src, Classifier: PrefixClassifier{}, Driver: drv, AllowAuthors: []string{"alice"}}

	n, err := b.PollOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.Len(t, drv.turns, 1)
	assert.Equal(t, "alice", drv.turns[0].author)
}

func TestBridge_DedupAcrossPolls(t *testing.T) {
	src := &fakeSource{replies: []Reply{{ID: "1", Author: "alice", Body: "continue"}}}
	drv := &recordingDriver{}
	b := &Bridge{Source: src, Classifier: PrefixClassifier{}, Driver: drv}

	n1, err := b.PollOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, n1)

	// Second poll returns the same reply (whole-thread source) — must not re-drive.
	n2, err := b.PollOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, n2)
	assert.Len(t, drv.turns, 1)
}

func TestBridge_DriverErrorIsBestEffort(t *testing.T) {
	src := &fakeSource{replies: []Reply{
		{ID: "1", Author: "alice", Body: "continue"},
	}}
	drv := &recordingDriver{err: errors.New("writer lock busy")}
	b := &Bridge{Source: src, Classifier: PrefixClassifier{}, Driver: drv}

	n, err := b.PollOnce(context.Background())
	assert.Equal(t, 0, n)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "writer lock busy")
}

func TestBridge_PollErrorWrapped(t *testing.T) {
	src := &fakeSource{key: transport.SessionKey{Transport: "jira", Thread: "X-1"}, err: errors.New("boom")}
	b := &Bridge{Source: src, Classifier: PrefixClassifier{}, Driver: &recordingDriver{}}
	_, err := b.PollOnce(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jira:X-1")
}
