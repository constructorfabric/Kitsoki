package app_test

// loader_providers_test.go — unit tests for the providers: block: parsing,
// ${VAR} substitution, agent/effect reference validation, and the
// empty-provider / unset-env fast-fail paths.
//
// All tests use LoadBytes (in-memory parse), which runs resolveProviders and
// validateProviderReferences as part of its pipeline.

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
)

// providersApp is a minimal app that declares an agent selecting a provider so
// reference validation has a site to check.
const providersAppHeader = `
app:
  id: test-providers
  version: 0.1.0
root: idle
states:
  idle:
    terminal: true
`

func TestProviders_ParsedAndSubstituted(t *testing.T) {
	t.Setenv("TEST_LOCAL_LLM_TOKEN", "sk-secret")
	yaml := providersAppHeader + `
providers:
  local_llm:
    backend: codex
    model: h200/gpt-oss-120b
    env:
      ANTHROPIC_BASE_URL: https://local-llm.example.com
      ANTHROPIC_AUTH_TOKEN: "${TEST_LOCAL_LLM_TOKEN}"
agents:
  helper:
    system_prompt: be helpful
    provider: local_llm
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	p, ok := def.Providers["local_llm"]
	if !ok {
		t.Fatal("providers: missing local_llm")
	}
	if p.Model != "h200/gpt-oss-120b" {
		t.Errorf("model: got %q", p.Model)
	}
	if p.Backend != "codex" {
		t.Errorf("backend: got %q", p.Backend)
	}
	if p.Env["ANTHROPIC_AUTH_TOKEN"] != "sk-secret" {
		t.Errorf("env.ANTHROPIC_AUTH_TOKEN not substituted: got %q", p.Env["ANTHROPIC_AUTH_TOKEN"])
	}
	if def.Agents["helper"].Provider != "local_llm" {
		t.Errorf("agent.provider: got %q", def.Agents["helper"].Provider)
	}
}

func TestProviders_UnsetEnvVarFailsFast(t *testing.T) {
	yaml := providersAppHeader + `
providers:
  local_llm:
    env:
      ANTHROPIC_AUTH_TOKEN: "${UNSET_PROVIDER_TOKEN_XYZ}"
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "UNSET_PROVIDER_TOKEN_XYZ") {
		t.Fatalf("expected unset-env error naming the var; got %v", err)
	}
}

func TestProviders_EmptyProviderRejected(t *testing.T) {
	yaml := providersAppHeader + `
providers:
  useless: {}
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "useless") {
		t.Fatalf("expected empty-provider error; got %v", err)
	}
}

func TestProviders_UnknownAgentRefRejected(t *testing.T) {
	yaml := providersAppHeader + `
providers:
  local_llm:
    model: m
agents:
  helper:
    system_prompt: hi
    provider: typo_provider
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "typo_provider") {
		t.Fatalf("expected unknown-provider error naming typo_provider; got %v", err)
	}
}

func TestProviders_EffectWithProviderValidated(t *testing.T) {
	yaml := `
app:
  id: test-providers-effect
  version: 0.1.0
hosts:
  - host.agent.decide
providers:
  local_llm:
    model: m
root: idle
states:
  idle:
    on_enter:
      - invoke: host.agent.decide
        with:
          provider: not_declared
          prompt: hi
    terminal: true
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "not_declared") {
		t.Fatalf("expected unknown effect-provider error naming not_declared; got %v", err)
	}
}

func TestAgentEffort_ValidAccepted(t *testing.T) {
	yaml := providersAppHeader + `
agents:
  judge:
    system_prompt: be helpful
    effort: xhigh
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if def.Agents["judge"].Effort != "xhigh" {
		t.Errorf("agent.effort: got %q", def.Agents["judge"].Effort)
	}
}

func TestAgentEffort_InvalidRejected(t *testing.T) {
	yaml := providersAppHeader + `
agents:
  judge:
    system_prompt: be helpful
    effort: turbo
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "turbo") {
		t.Fatalf("expected invalid-effort error naming turbo; got %v", err)
	}
}

func TestProviderEffort_InvalidRejected(t *testing.T) {
	yaml := providersAppHeader + `
providers:
  local_llm:
    model: m
    effort: ludicrous
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "ludicrous") {
		t.Fatalf("expected invalid provider-effort error naming ludicrous; got %v", err)
	}
}

func TestProviderEffort_OnlyEffortAccepted(t *testing.T) {
	yaml := providersAppHeader + `
providers:
  effort_only:
    effort: high
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if def.Providers["effort_only"].Effort != "high" {
		t.Errorf("provider.effort: got %q", def.Providers["effort_only"].Effort)
	}
}

func TestProviderBackend_OnlyBackendAccepted(t *testing.T) {
	yaml := providersAppHeader + `
providers:
  codex_native:
    backend: codex
`
	def, err := app.LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if def.Providers["codex_native"].Backend != "codex" {
		t.Errorf("provider.backend: got %q", def.Providers["codex_native"].Backend)
	}
}

func TestProviderBackend_InvalidRejected(t *testing.T) {
	yaml := providersAppHeader + `
providers:
  strange:
    backend: not-a-backend
`
	_, err := app.LoadBytes([]byte(yaml))
	if err == nil || !strings.Contains(err.Error(), "not-a-backend") {
		t.Fatalf("expected invalid provider-backend error naming not-a-backend; got %v", err)
	}
}

func TestProviders_AbsentIsClean(t *testing.T) {
	def, err := app.LoadBytes([]byte(providersAppHeader))
	if err != nil {
		t.Fatalf("LoadBytes: %v", err)
	}
	if len(def.Providers) != 0 {
		t.Errorf("expected no providers; got %v", def.Providers)
	}
}
