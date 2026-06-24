package host

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakePrompter is a test OperatorPrompter that records the call and returns a
// canned answer (or error).
type fakePrompter struct {
	gotSession   string
	gotQuestions []OperatorQuestion
	answers      map[string]any
	err          error
}

func (f *fakePrompter) Ask(_ context.Context, sessionID string, qs []OperatorQuestion) (map[string]any, error) {
	f.gotSession = sessionID
	f.gotQuestions = qs
	return f.answers, f.err
}

func TestOperatorPrompterSeam(t *testing.T) {
	t.Run("absent by default — headless tool-denied posture", func(t *testing.T) {
		ctx := context.Background()
		p, ok := OperatorPrompterFrom(ctx)
		require.False(t, ok)
		require.Nil(t, p)
		require.False(t, OperatorInteractive(ctx), "no prompter ⇒ not interactive")
	})

	t.Run("nil prompter is a no-op (stays headless)", func(t *testing.T) {
		ctx := WithOperatorPrompter(context.Background(), nil)
		require.False(t, OperatorInteractive(ctx))
	})

	t.Run("installed prompter is interactive and round-trips", func(t *testing.T) {
		fake := &fakePrompter{answers: map[string]any{"Pick a fruit": "Apple"}}
		ctx := WithOperatorPrompter(context.Background(), fake)

		require.True(t, OperatorInteractive(ctx))
		got, ok := OperatorPrompterFrom(ctx)
		require.True(t, ok)
		require.Same(t, fake, got)

		qs := []OperatorQuestion{{
			Question:    "Pick a fruit",
			Header:      "Fruit",
			Options:     []OperatorOption{{Label: "Apple", Description: "a pome"}, {Label: "Pear", Description: "also a pome"}},
			MultiSelect: false,
		}}
		answers, err := got.Ask(ctx, "sess-1", qs)
		require.NoError(t, err)
		require.Equal(t, "Apple", answers["Pick a fruit"])
		require.Equal(t, "sess-1", fake.gotSession)
		require.Equal(t, qs, fake.gotQuestions)
	})

	t.Run("prompter error propagates (operator cancelled / timed out)", func(t *testing.T) {
		sentinel := errors.New("operator did not answer")
		fake := &fakePrompter{err: sentinel}
		ctx := WithOperatorPrompter(context.Background(), fake)
		p, _ := OperatorPrompterFrom(ctx)
		_, err := p.Ask(ctx, "sess-2", nil)
		require.ErrorIs(t, err, sentinel)
	})

	t.Run("multiselect answer is a []string", func(t *testing.T) {
		fake := &fakePrompter{answers: map[string]any{"Toppings": []string{"Cheese", "Olives"}}}
		ctx := WithOperatorPrompter(context.Background(), fake)
		p, _ := OperatorPrompterFrom(ctx)
		answers, err := p.Ask(ctx, "s", []OperatorQuestion{{Question: "Toppings", MultiSelect: true}})
		require.NoError(t, err)
		require.Equal(t, []string{"Cheese", "Olives"}, answers["Toppings"])
	})
}
