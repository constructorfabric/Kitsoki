package main

import (
	"context"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/webconfig"
)

// TestFirstRunProviderHint verifies change 0.4: a fresh run with no provider
// gets an actionable message (not a silent replay fallback), and a run with a
// provider gets nothing.
func TestFirstRunProviderHint(t *testing.T) {
	if got := firstRunProviderHint(false, false); got == "" {
		t.Fatal("expected an actionable hint when no provider is configured, got empty")
	} else {
		for _, want := range []string{"no agent provider found", "ANTHROPIC_API_KEY", "claude", "--agent codex", "harness profile", "replay"} {
			if !contains(got, want) {
				t.Errorf("hint missing %q; hint was:\n%s", want, got)
			}
		}
	}
	if got := firstRunProviderHint(true, false); got != "" {
		t.Errorf("expected no hint when claude is on PATH, got:\n%s", got)
	}
	if got := firstRunProviderHint(false, true); got != "" {
		t.Errorf("expected no hint when a credential is present, got:\n%s", got)
	}
}

func TestAutoSelectHarnessUsesConfiguredAgentBackend(t *testing.T) {
	clearDefaultHarnessProviders(t)

	got, err := autoSelectHarness("codex")
	if err != nil {
		t.Fatalf("autoSelectHarness(codex): %v", err)
	}
	if got != "claude" {
		t.Fatalf("autoSelectHarness(codex) = %q, want claude CLI harness", got)
	}
}

func TestAutoSelectHarnessNoProviderErrorsInsteadOfReplay(t *testing.T) {
	clearDefaultHarnessProviders(t)

	got, err := autoSelectHarness("")
	if err == nil {
		t.Fatalf("autoSelectHarness with no provider = %q, want error", got)
	}
	msg := err.Error()
	for _, want := range []string{"no agent provider found", "--agent codex", "--harness replay --recording"} {
		if !contains(msg, want) {
			t.Fatalf("error missing %q; error was:\n%s", want, msg)
		}
	}
	if contains(msg, "--recording is required") {
		t.Fatalf("auto selection leaked replay-harness validation instead of provider setup guidance:\n%s", msg)
	}
}

func TestBuildHarnessDefaultCodexBackendDoesNotSelectReplay(t *testing.T) {
	clearDefaultHarnessProviders(t)
	t.Setenv(host.CodexBinEnv, "/tmp/kitsoki-test-codex")

	h, err := buildHarness("", "", "codex", "", "", &app.AppDef{})
	if err != nil {
		t.Fatalf("buildHarness default codex backend: %v", err)
	}
	if h == nil {
		t.Fatal("buildHarness default codex backend returned nil harness")
	}
	_ = h.Close()
}

func TestBugFilingAuthStartupNotice(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("HOME", t.TempDir())
	restoreGHCLI := host.SetGHCLITokenForTest(func(context.Context) string { return "" })
	defer restoreGHCLI()

	got := bugFilingAuthStartupNotice(context.Background(), "o/r")
	for _, want := range []string{"GitHub bug filing is unavailable", "o/r", "Filing bugs is critical", "kitsoki gh-agent login"} {
		if !contains(got, want) {
			t.Fatalf("notice missing %q; notice was:\n%s", want, got)
		}
	}

	if got := bugFilingAuthStartupNotice(context.Background(), ""); got != "" {
		t.Fatalf("local bug filing should not warn, got:\n%s", got)
	}
	t.Setenv("GH_TOKEN", "test-token")
	if got := bugFilingAuthStartupNotice(context.Background(), "o/r"); got != "" {
		t.Fatalf("configured GitHub auth should not warn, got:\n%s", got)
	}
}

func TestRunAsUserStartupNotice(t *testing.T) {
	if got := runAsUserStartupNotice(webconfig.WebConfig{}, "darwin"); got != "" {
		t.Fatalf("disabled run_as_user should not warn, got:\n%s", got)
	}

	incomplete := webconfig.WebConfig{
		AgentUserDelegation: &webconfig.AgentUserDelegationConfig{
			Enabled:   true,
			RunAsUser: "kitsoki-agent",
		},
	}
	if got := runAsUserStartupNotice(incomplete, "darwin"); got != "" {
		t.Fatalf("disabled run_as_user should not warn for incomplete config, got:\n%s", got)
	}

	configured := webconfig.WebConfig{
		AgentUserDelegation: &webconfig.AgentUserDelegationConfig{
			Enabled:    true,
			RunAsUser:  "kitsoki-agent",
			WrapperBin: "/Users/Shared/kitsoki/agent-bin",
		},
	}
	if got := runAsUserStartupNotice(configured, "darwin"); got != "" {
		t.Fatalf("disabled run_as_user should not warn for configured delegation, got:\n%s", got)
	}
	if got := runAsUserStartupNotice(webconfig.WebConfig{}, "linux"); got != "" {
		t.Fatalf("non-darwin should not warn, got:\n%s", got)
	}
}

func TestSetupWarningsFromConfigSuppressesRunAsUserWhileDisabled(t *testing.T) {
	warnings := setupWarningsFromConfig(webconfig.WebConfig{}, "darwin")
	if len(warnings) != 0 {
		t.Fatalf("disabled run_as_user should not create setup warnings, got %#v", warnings)
	}

	incomplete := webconfig.WebConfig{
		AgentUserDelegation: &webconfig.AgentUserDelegationConfig{
			Enabled:   true,
			RunAsUser: "kitsoki-agent",
		},
	}
	if got := setupWarningsFromConfig(incomplete, "darwin"); len(got) != 0 {
		t.Fatalf("disabled run_as_user should suppress incomplete setup warning, got %#v", got)
	}

	configured := webconfig.WebConfig{
		AgentUserDelegation: &webconfig.AgentUserDelegationConfig{
			Enabled:    true,
			RunAsUser:  "kitsoki-agent",
			WrapperBin: "/Users/Shared/kitsoki/agent-bin",
		},
	}
	if got := setupWarningsFromConfig(configured, "darwin"); len(got) != 0 {
		t.Fatalf("disabled run_as_user should not create configured setup warning, got %#v", got)
	}
	if got := setupWarningsFromConfig(webconfig.WebConfig{}, "linux"); len(got) != 0 {
		t.Fatalf("non-darwin should suppress setup warning, got %#v", got)
	}
}

func TestSetupWarningsFromRuntimeConfigWarnsWhenBugPrivacyRequired(t *testing.T) {
	warnings := setupWarningsFromRuntimeConfig(webconfig.WebConfig{}, "linux", bugPrivacyRuntimeConfig{}, true)
	if len(warnings) != 1 {
		t.Fatalf("expected one bug privacy setup warning, got %#v", warnings)
	}
	if warnings[0].ID != "bug-privacy-checker" {
		t.Fatalf("warning ID = %q", warnings[0].ID)
	}
	if !contains(warnings[0].Body, "deterministic scrubbing") || !contains(warnings[0].ActionCommand, "harness_profiles") {
		t.Fatalf("warning is not actionable enough: %#v", warnings[0])
	}
}

func TestSetupWarningsFromRuntimeConfigSuppressesBugPrivacyWhenCheckerAvailable(t *testing.T) {
	withDefaultProfile := setupWarningsFromRuntimeConfig(webconfig.WebConfig{
		DefaultProfile: "codex-native",
		HarnessProfiles: map[string]webconfig.HarnessProfile{
			"codex-native": {
				Backend: "codex",
				Model:   "gpt-5.5",
				Models:  []string{"gpt-5.5"},
			},
		},
	}, "linux", bugPrivacyRuntimeConfig{}, true)
	if len(withDefaultProfile) != 0 {
		t.Fatalf("configured default profile should suppress bug privacy warning, got %#v", withDefaultProfile)
	}

	withActiveBackend := setupWarningsFromRuntimeConfig(webconfig.WebConfig{}, "linux", bugPrivacyRuntimeConfig{
		AgentBackend: "codex",
	}, true)
	if len(withActiveBackend) != 0 {
		t.Fatalf("active backend should suppress bug privacy warning, got %#v", withActiveBackend)
	}

	withDefaultLive := setupWarningsFromRuntimeConfig(webconfig.WebConfig{}, "linux", bugPrivacyRuntimeConfig{
		UseDefaultLiveLadder: true,
	}, true)
	if len(withDefaultLive) != 0 {
		t.Fatalf("live default ladder should suppress bug privacy warning, got %#v", withDefaultLive)
	}

	notRequired := setupWarningsFromRuntimeConfig(webconfig.WebConfig{}, "linux", bugPrivacyRuntimeConfig{}, false)
	if len(notRequired) != 0 {
		t.Fatalf("non-GitHub bug filing should not warn about privacy checker, got %#v", notRequired)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func clearDefaultHarnessProviders(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
}
