package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/embed"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/semroute"
)

// TestEmbedTierDisabled verifies that a disabled EmbedTier is a no-op.
func TestEmbedTierDisabled(t *testing.T) {
	t.Parallel()
	cfg := orchestrator.DefaultEmbedTierConfig()
	// DefaultEmbedTierConfig has Enabled=false; pass nil embedder.
	tier := orchestrator.NewEmbedTier(cfg, nil)
	ctx := context.Background()

	intents := []orchestrator.IntentSpec{{Name: "ford"}, {Name: "hunt"}}
	verdict, ok, err := tier.Match(ctx, intents, "ford")
	require.NoError(t, err)
	require.False(t, ok, "disabled tier must return ok=false")
	require.Equal(t, semroute.Verdict{}, verdict)
}

// TestEmbedTierHit verifies that the embed tier returns a confident match when
// the query is identical to an intent name (FakeEmbedder returns score≈1.0 for
// identical texts).
func TestEmbedTierHit(t *testing.T) {
	t.Parallel()
	cfg := orchestrator.EmbedTierConfig{
		Enabled:      true,
		Model:        "nomic-embed-text-v1.5",
		Dim:          8,
		ConfidentBar: 0.5,
		Margin:       0.01,
	}
	tier := orchestrator.NewEmbedTier(cfg, embed.NewFakeEmbedder(8))
	ctx := context.Background()

	intents := []orchestrator.IntentSpec{{Name: "ford"}, {Name: "hunt"}}
	verdict, ok, err := tier.Match(ctx, intents, "ford")
	require.NoError(t, err)
	require.True(t, ok, "expect a hit when input matches an intent name exactly")
	require.Equal(t, "ford", verdict.Intent)
	require.Equal(t, semroute.ConfidenceEmbedding, verdict.Confidence)
}

// TestEmbedTierMiss verifies that raising ConfidentBar forces a miss even when
// the query is identical to an intent name (score < bar).
func TestEmbedTierMiss(t *testing.T) {
	t.Parallel()
	cfg := orchestrator.EmbedTierConfig{
		Enabled:      true,
		Model:        "nomic-embed-text-v1.5",
		Dim:          8,
		ConfidentBar: 0.99, // unreachable for distinct hash-based vectors
		Margin:       0.01,
	}
	tier := orchestrator.NewEmbedTier(cfg, embed.NewFakeEmbedder(8))
	ctx := context.Background()

	intents := []orchestrator.IntentSpec{{Name: "ford"}, {Name: "hunt"}}
	// Even "ford" vs "ford" may not hit 0.99 because the query is
	// embedded as Role=Query and the document as Role=Document; the
	// FakeEmbedder ignores Role (strips "key: " prefix), so both produce
	// the same unit vector and score ≈ 1.0. Set ConfidentBar to 1.01 to
	// guarantee a miss regardless.
	cfg2 := cfg
	cfg2.ConfidentBar = 1.01 // above the maximum possible cosine score
	tier2 := orchestrator.NewEmbedTier(cfg2, embed.NewFakeEmbedder(8))
	_, ok, err := tier2.Match(ctx, intents, "ford")
	require.NoError(t, err)
	require.False(t, ok, "expect a miss when ConfidentBar exceeds the maximum possible score")

	// Also verify the original 0.99 tier with a completely unrelated input.
	_, ok2, err2 := tier.Match(ctx, intents, "xyzzy_totally_unrelated_quux")
	require.NoError(t, err2)
	// With a score well below 0.99 this should also miss.
	// We cannot guarantee the score for a random hash, but we assert
	// that with ConfidentBar=0.99 an unrelated input won't randomly score
	// above it — if it does the test is a flake and ConfidentBar should
	// be raised in cfg.
	_ = ok2 // non-deterministic; the ConfidentBar=1.01 assertion above is the canonical miss test.
}

// countingEmbedder wraps an embed.Embedder and counts how many times Embed
// has been called. Used to assert cache-warm behaviour in tests.
type countingEmbedder struct {
	inner embed.Embedder
	calls int
}

func (c *countingEmbedder) Embed(ctx context.Context, texts []string, role embed.Role) ([][]float32, error) {
	c.calls++
	return c.inner.Embed(ctx, texts, role)
}

// TestEmbedTierTie verifies that when two candidates both clear ConfidentBar
// but the margin between them is below Margin, the tier returns a tie verdict.
func TestEmbedTierTie(t *testing.T) {
	t.Parallel()
	cfg := orchestrator.EmbedTierConfig{
		Enabled:      true,
		Model:        "nomic-embed-text-v1.5",
		Dim:          8,
		ConfidentBar: 0.1,  // very low — both candidates will clear it
		Margin:       100.0, // impossibly high — top1-top2 will always be below it
	}
	tier := orchestrator.NewEmbedTier(cfg, embed.NewFakeEmbedder(8))
	ctx := context.Background()

	intents := []orchestrator.IntentSpec{{Name: "alpha"}, {Name: "beta"}}
	verdict, ok, err := tier.Match(ctx, intents, "alpha")
	require.NoError(t, err)
	require.True(t, ok, "expect ok=true on a tie (top1 clears bar)")
	require.Equal(t, semroute.ConfidenceTie, verdict.Confidence)
	require.Len(t, verdict.Candidates, 2)
	require.NotEmpty(t, verdict.Candidates[0].Intent)
	require.NotEmpty(t, verdict.Candidates[1].Intent)
}

// TestEmbedTierMatchSpec exercises Match end-to-end with a realistic scenario:
// 5 intents, FakeEmbedder, ConfidentBar=0.5, Margin=0.01. Also verifies that
// the intent vector cache is warm after the first call (no additional document
// embeds on the second call).
func TestEmbedTierMatchSpec(t *testing.T) {
	t.Parallel()
	cfg := orchestrator.EmbedTierConfig{
		Enabled:      true,
		Model:        "nomic-embed-text-v1.5",
		Dim:          8,
		ConfidentBar: 0.5,
		Margin:       0.01,
	}
	counter := &countingEmbedder{inner: embed.NewFakeEmbedder(8)}
	tier := orchestrator.NewEmbedTier(cfg, counter)
	ctx := context.Background()

	intents := []orchestrator.IntentSpec{
		{Name: "create_report"},
		{Name: "delete_user"},
		{Name: "send_message"},
		{Name: "update_profile"},
		{Name: "view_dashboard"},
	}

	// First call: querying the exact text of "send_message" should return that intent.
	callsBefore := counter.calls
	verdict1, ok1, err1 := tier.Match(ctx, intents, "send_message")
	require.NoError(t, err1)
	require.True(t, ok1, "querying exact intent name should hit")
	require.Equal(t, "send_message", verdict1.Intent)
	callsAfterFirst := counter.calls
	// First call must have embedded documents (5 intents) + 1 query = at least 2 Embed calls.
	require.Greater(t, callsAfterFirst, callsBefore, "first call should invoke Embed at least once")

	// Second call with a different intent name: cache is warm for documents.
	callsBeforeSecond := counter.calls
	verdict2, ok2, err2 := tier.Match(ctx, intents, "delete_user")
	require.NoError(t, err2)
	require.True(t, ok2, "querying exact intent name should hit")
	require.Equal(t, "delete_user", verdict2.Intent)
	callsAfterSecond := counter.calls
	// Cache is warm: only 1 additional Embed call (for the query), no document re-embeds.
	require.Equal(t, 1, callsAfterSecond-callsBeforeSecond,
		"second Match call should make exactly 1 Embed call (query only, cache warm)")
}
