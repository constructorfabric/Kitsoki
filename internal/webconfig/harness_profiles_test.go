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
		// Unset env on the SELECTED default_profile is fatal — the boot profile
		// must resolve. (A non-default profile with an unset env is NOT fatal;
		// see TestLoad_HarnessProfiles_UnsetEnvOnNonDefaultIsDropped.)
		"unset env var on the default_profile is a hard error": `
default_profile: synthetic
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

// TestLoad_HarnessProfiles_UnsetEnvOnNonDefaultIsDropped is the regression for
// the VS Code "backend exited (code 1) before becoming healthy" failure: a
// secret-bearing profile in a (gitignored) override references an env var that a
// GUI-launched editor's environment lacks. That must NOT kill config load when
// the profile isn't the selected default — it is dropped from the usable set and
// the config loads with the default profile intact. (In the wild: `claude-native`
// boots while `synthetic-claude` referencing ${SYNTHETIC_API_KEY} is dropped.)
func TestLoad_HarnessProfiles_UnsetEnvOnNonDefaultIsDropped(t *testing.T) {
	os.Unsetenv("DEFINITELY_UNSET_KEY_XYZ")
	p := writeConfig(t, `
default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
  synthetic-claude:
    backend: claude
    env: { ANTHROPIC_AUTH_TOKEN: "${DEFINITELY_UNSET_KEY_XYZ}" }
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load must not fail when an unused profile's env var is unset: %v", err)
	}
	if _, ok := cfg.HarnessProfiles["synthetic-claude"]; ok {
		t.Fatal("synthetic-claude should be dropped (its env var is unset)")
	}
	if _, ok := cfg.HarnessProfiles["claude-native"]; !ok {
		t.Fatal("claude-native (the default) must survive and remain usable")
	}
	if cfg.DefaultProfile != "claude-native" {
		t.Fatalf("default_profile = %q, want claude-native", cfg.DefaultProfile)
	}
}

// writeLocalBeside writes a .kitsoki.local.yaml next to an existing base path
// returned by writeConfig, so Load picks it up as the override.
func writeLocalBeside(t *testing.T, basePath, body string) {
	t.Helper()
	if err := os.WriteFile(LocalConfigPath(basePath), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLocalConfigPath(t *testing.T) {
	cases := map[string]string{
		".kitsoki.yaml":      ".kitsoki.local.yaml",
		"foo/bar.yaml":       "foo/bar.local.yaml",
		"/abs/.kitsoki.yaml": "/abs/.kitsoki.local.yaml",
		"noext":              "noext.local",
	}
	for in, want := range cases {
		if got := LocalConfigPath(in); got != want {
			t.Errorf("LocalConfigPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The local override deep-merges onto the base: it adds new profiles, replaces
// a profile of the same name whole, and overrides default_profile + story_dirs,
// while base-only profiles survive untouched.
func TestLoad_LocalOverride_DeepMerge(t *testing.T) {
	t.Setenv("SYNTHETIC_API_KEY", "sk-secret")
	base := writeConfig(t, `
story_dirs: [./stories]
default_profile: claude-native
harness_profiles:
  claude-native:
    backend: claude
    model: sonnet
    models: [opus, sonnet, haiku]
`)
	writeLocalBeside(t, base, `
story_dirs: [./mine]
default_profile: synthetic-claude
harness_profiles:
  # Replaces the base claude-native whole (model bumped to opus).
  claude-native:
    backend: claude
    model: opus
    models: [opus, sonnet, haiku]
  # New, secret-bearing profile only the local file declares.
  synthetic-claude:
    backend: claude
    env:
      ANTHROPIC_AUTH_TOKEN: "${SYNTHETIC_API_KEY}"
`)
	cfg, err := Load(base)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultProfile != "synthetic-claude" {
		t.Errorf("default_profile = %q, want synthetic-claude (local wins)", cfg.DefaultProfile)
	}
	if len(cfg.StoryDirs) != 1 || cfg.StoryDirs[0] != "./mine" {
		t.Errorf("story_dirs = %v, want [./mine] (local replaces)", cfg.StoryDirs)
	}
	if got := cfg.HarnessProfiles["claude-native"].Model; got != "opus" {
		t.Errorf("claude-native.model = %q, want opus (local replaced whole)", got)
	}
	if got := cfg.HarnessProfiles["synthetic-claude"].Env["ANTHROPIC_AUTH_TOKEN"]; got != "sk-secret" {
		t.Errorf("local profile ${VAR} not expanded post-merge: %q", got)
	}
	// default_profile naming a profile only the LOCAL file declares is legal
	// because validation runs after the merge.
}

// A base-only config (no local file beside it) loads exactly as before — the
// override is purely additive and absent by default.
func TestLoad_NoLocalFile_BaseUnchanged(t *testing.T) {
	cfg, err := Load(writeConfig(t, "default_profile: claude-native\nharness_profiles:\n  claude-native: { backend: claude }\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultProfile != "claude-native" || len(cfg.HarnessProfiles) != 1 {
		t.Fatalf("base-only load changed: %+v", cfg)
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
