package host

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEnvWithProvider_NoOpWhenEmpty proves the non-provider path is byte-identical:
// an empty/nil override returns the input env unchanged.
func TestEnvWithProvider_NoOpWhenEmpty(t *testing.T) {
	in := []string{"PATH=/usr/bin", "HOME=/home/x"}
	require.Equal(t, in, envWithProvider(in, nil))
	require.Equal(t, in, envWithProvider(in, map[string]string{}))
}

// TestEnvWithProvider_OverridesAndAppends proves a provider env entry replaces an
// existing key in place and a new key is appended, with unrelated entries intact.
func TestEnvWithProvider_OverridesAndAppends(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_BASE_URL=https://api.anthropic.com",
		"HOME=/home/x",
	}
	got := envWithProvider(in, map[string]string{
		"ANTHROPIC_BASE_URL":   "https://local-llm.example.com",
		"ANTHROPIC_AUTH_TOKEN": "sk-test",
	})

	// Existing key replaced in place (not duplicated).
	require.Contains(t, got, "ANTHROPIC_BASE_URL=https://local-llm.example.com")
	require.NotContains(t, got, "ANTHROPIC_BASE_URL=https://api.anthropic.com")
	require.Equal(t, 1, countPrefix(got, "ANTHROPIC_BASE_URL="))
	// New key appended.
	require.Contains(t, got, "ANTHROPIC_AUTH_TOKEN=sk-test")
	// Unrelated entries survive.
	require.Contains(t, got, "PATH=/usr/bin")
	require.Contains(t, got, "HOME=/home/x")
}

// TestEnvWithProvider_Deterministic proves repeated calls produce identical
// output (no map-iteration nondeterminism leaking into the env slice).
func TestEnvWithProvider_Deterministic(t *testing.T) {
	in := []string{"A=1", "B=2"}
	prov := map[string]string{"Z": "z", "Y": "y", "X": "x"}
	first := envWithProvider(in, prov)
	for i := 0; i < 10; i++ {
		require.Equal(t, first, envWithProvider(in, prov), "iteration %d differs", i)
	}
}

func countPrefix(env []string, prefix string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
		}
	}
	return n
}

// TestApplyProvider_Unselected leaves ctx and agent untouched when neither the
// effect nor the agent names a provider.
func TestApplyProvider_Unselected(t *testing.T) {
	ctx := WithProviders(context.Background(), map[string]Provider{
		"local": {Model: "h200/x", Env: map[string]string{"ANTHROPIC_BASE_URL": "u"}},
	})
	agent := Agent{Model: "claude-opus-4-8"}
	gotCtx, gotAgent := applyProvider(ctx, map[string]any{}, agent)
	require.Equal(t, agent, gotAgent)
	require.Nil(t, AgentProviderEnvFromCtx(gotCtx))
}

// TestApplyProvider_FromAgent applies the agent-declared provider: env is
// installed and the provider model fills in for an agent with no explicit model.
func TestApplyProvider_FromAgent(t *testing.T) {
	ctx := WithProviders(context.Background(), map[string]Provider{
		"local": {Model: "h200/gpt-oss-120b", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://local"}},
	})
	agent := Agent{Provider: "local"} // no explicit model
	gotCtx, gotAgent := applyProvider(ctx, map[string]any{}, agent)
	require.Equal(t, "h200/gpt-oss-120b", gotAgent.Model, "provider model fills empty agent model")
	require.Equal(t, map[string]string{"ANTHROPIC_BASE_URL": "https://local"}, AgentProviderEnvFromCtx(gotCtx))
}

// TestApplyProvider_ExplicitModelWins keeps an agent's explicit model even when
// the provider declares its own default model.
func TestApplyProvider_ExplicitModelWins(t *testing.T) {
	ctx := WithProviders(context.Background(), map[string]Provider{
		"local": {Model: "h200/gpt-oss-120b"},
	})
	agent := Agent{Provider: "local", Model: "claude-opus-4-8"}
	_, gotAgent := applyProvider(ctx, map[string]any{}, agent)
	require.Equal(t, "claude-opus-4-8", gotAgent.Model)
}

// TestApplyProvider_ExplicitCallProviderWinsModelEffortAndBackend proves
// per-invoke provider selection can route one call independently of the
// session/agent defaults.
func TestApplyProvider_ExplicitCallProviderWinsModelEffortAndBackend(t *testing.T) {
	ctx := WithProviders(context.Background(), map[string]Provider{
		"codex_eval": {Backend: "codex", Model: "gpt-5.5", Effort: "high"},
	})
	agent := Agent{Provider: "claude_eval", Model: "opus", Effort: "medium"}
	gotCtx, gotAgent := applyProvider(ctx, map[string]any{"provider": "codex_eval"}, agent)
	require.Equal(t, "gpt-5.5", gotAgent.Model)
	require.Equal(t, "high", gotAgent.Effort)
	require.Equal(t, "codex", AgentBackendFromContext(gotCtx).Name())
}

// TestApplyProvider_EffectArgWinsOverAgent proves the effect's with.provider
// overrides the agent-declared provider (precedence mirrors system_prompt/tools).
func TestApplyProvider_EffectArgWinsOverAgent(t *testing.T) {
	ctx := WithProviders(context.Background(), map[string]Provider{
		"agentprov":  {Env: map[string]string{"ANTHROPIC_BASE_URL": "agent"}},
		"effectprov": {Env: map[string]string{"ANTHROPIC_BASE_URL": "effect"}},
	})
	agent := Agent{Provider: "agentprov"}
	gotCtx, _ := applyProvider(ctx, map[string]any{"provider": "effectprov"}, agent)
	require.Equal(t, map[string]string{"ANTHROPIC_BASE_URL": "effect"}, AgentProviderEnvFromCtx(gotCtx))
}

// TestApplyProvider_UnknownNameFallsBackToAmbient is the no-loader test-scaffold
// path: an unknown provider name is a no-op (ambient env), never a panic.
func TestApplyProvider_UnknownNameFallsBackToAmbient(t *testing.T) {
	ctx := WithProviders(context.Background(), map[string]Provider{"local": {Model: "m"}})
	agent := Agent{Provider: "does-not-exist"}
	gotCtx, gotAgent := applyProvider(ctx, map[string]any{}, agent)
	require.Equal(t, agent, gotAgent)
	require.Nil(t, AgentProviderEnvFromCtx(gotCtx))
}
