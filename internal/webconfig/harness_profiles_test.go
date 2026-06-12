package webconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, ".kitsoki.yaml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A valid config loads profiles, expands ${VAR} env against the process
// environment, and accepts a default_profile that names a declared profile.
func TestLoad_HarnessProfiles_Valid(t *testing.T) {
	t.Setenv("SYNTHETIC_API_KEY", "sk-secret")
	p := writeConfig(t, `
default_profile: synthetic-claude
harness_profiles:
  claude-native:
    backend: claude
  synthetic-claude:
    backend: claude
    model: hf:Qwen/Qwen2.5-Coder-32B-Instruct
    models: [hf:Qwen/Qwen2.5-Coder-32B-Instruct]
    env:
      ANTHROPIC_BASE_URL: https://api.synthetic.new/anthropic
      ANTHROPIC_AUTH_TOKEN: "${SYNTHETIC_API_KEY}"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultProfile != "synthetic-claude" {
		t.Fatalf("default_profile = %q", cfg.DefaultProfile)
	}
	sc := cfg.HarnessProfiles["synthetic-claude"]
	if sc.Env["ANTHROPIC_AUTH_TOKEN"] != "sk-secret" {
		t.Fatalf("${SYNTHETIC_API_KEY} not expanded: %q", sc.Env["ANTHROPIC_AUTH_TOKEN"])
	}
}

func TestLoad_HarnessProfiles_Errors(t *testing.T) {
	cases := map[string]string{
		"unset env var is a hard error": `
harness_profiles:
  synthetic:
    backend: claude
    env: { ANTHROPIC_AUTH_TOKEN: "${DEFINITELY_UNSET_KEY_XYZ}" }
`,
		"invalid backend rejected": `
harness_profiles:
  bad: { backend: gpt }
`,
		"dangling default_profile rejected": `
default_profile: nope
harness_profiles:
  claude-native: { backend: claude }
`,
		"model off its own catalog rejected": `
harness_profiles:
  p:
    backend: claude
    model: c
    models: [a, b]
`,
		"invalid effort rejected": `
harness_profiles:
  p: { backend: claude, efforts: [low, turbo] }
`,
		"default effort off its catalog rejected": `
harness_profiles:
  p: { backend: claude, effort: max, efforts: [low, medium] }
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			os.Unsetenv("DEFINITELY_UNSET_KEY_XYZ")
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Fatalf("expected load error for %q", name)
			}
		})
	}
}

// A config with no harness_profiles block loads to a zero-profiles WebConfig —
// the legacy path is untouched.
func TestLoad_NoProfiles_LegacyUntouched(t *testing.T) {
	cfg, err := Load(writeConfig(t, "story_dirs: [./stories]\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.HarnessProfiles) != 0 || cfg.DefaultProfile != "" {
		t.Fatalf("expected no profiles, got %+v / %q", cfg.HarnessProfiles, cfg.DefaultProfile)
	}
}
