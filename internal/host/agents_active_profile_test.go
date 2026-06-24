package host

import (
	"context"
	"testing"
)

// With no explicit provider on the call, applyProvider falls back to the active
// harness profile: it fills the empty agent model and installs the profile env on
// the context (which the exec layer merges onto the forked CLI for every backend).
func TestApplyProvider_ActiveProfileFallback(t *testing.T) {
	ctx := WithActiveProfile(context.Background(), ActiveProfile{
		Name: "synthetic-codex",
		Provider: Provider{
			Model: "hf:Qwen/Qwen2.5-Coder-32B-Instruct",
			Env:   map[string]string{"OPENAI_BASE_URL": "https://api.synthetic.new/openai"},
		},
	})

	ctx, agent := applyProvider(ctx, map[string]any{}, Agent{})
	if agent.Model != "hf:Qwen/Qwen2.5-Coder-32B-Instruct" {
		t.Fatalf("active profile model not applied: %q", agent.Model)
	}
	if env := AgentProviderEnvFromCtx(ctx); env["OPENAI_BASE_URL"] != "https://api.synthetic.new/openai" {
		t.Fatalf("active profile env not installed: %+v", env)
	}
	if ActiveProfileNameFromCtx(ctx) != "synthetic-codex" {
		t.Fatalf("active profile name not retrievable for trace stamping")
	}
}

// An active harness profile is the operator-selected provider/model for the
// session, so it supersedes story-local model defaults. Otherwise selecting
// synthetic-claude would still pass pinned Claude model names like
// claude-sonnet-4-6 to the synthetic endpoint.
func TestApplyProvider_ActiveProfileModelBeatsAgentModel(t *testing.T) {
	ctx := WithActiveProfile(context.Background(), ActiveProfile{
		Name:     "synthetic-claude",
		Provider: Provider{Model: "profile-model"},
	})
	_, agent := applyProvider(ctx, map[string]any{}, Agent{Model: "opus"})
	if agent.Model != "profile-model" {
		t.Fatalf("active profile model should win over agent default: got %q", agent.Model)
	}
}

// A named provider on the call takes precedence over the active profile fallback.
func TestApplyProvider_NamedProviderBeatsProfile(t *testing.T) {
	ctx := WithProviders(context.Background(), map[string]Provider{
		"openrouter": {Model: "named-model", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://openrouter"}},
	})
	ctx = WithActiveProfile(ctx, ActiveProfile{
		Name:     "synthetic-claude",
		Provider: Provider{Model: "profile-model", Env: map[string]string{"ANTHROPIC_BASE_URL": "https://synthetic"}},
	})

	ctx, agent := applyProvider(ctx, map[string]any{"provider": "openrouter"}, Agent{})
	if agent.Model != "named-model" {
		t.Fatalf("named provider model should win: got %q", agent.Model)
	}
	if env := AgentProviderEnvFromCtx(ctx); env["ANTHROPIC_BASE_URL"] != "https://openrouter" {
		t.Fatalf("named provider env should win: %+v", env)
	}
}
