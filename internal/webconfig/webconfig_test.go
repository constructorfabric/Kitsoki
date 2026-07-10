package webconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// minimalStory is the smallest manifest app.Load accepts: app id/version, a
// root, and one state. No agents/host/agent references so the load is pure.
const minimalStory = `app:
  id: %s
  version: 0.1.0
  title: %q

root: idle

states:
  idle:
    description: "Idle"
    view: "Idle."
`

const malformedStory = `app:
  id: broken
  version: 0.1.0

root: nowhere

states:
  idle:
    description: "Idle"
    view: "Idle."
`

func writeStory(t *testing.T, dir, id, title string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "app.yaml")
	content := []byte(fmt.Sprintf(minimalStory, id, title))
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs %s: %v", path, err)
	}
	return abs
}

func TestLoad_MissingFileIsNotError(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("missing config should not error: %v", err)
	}
	if len(cfg.StoryDirs) != 0 {
		t.Fatalf("missing config should yield empty StoryDirs, got %v", cfg.StoryDirs)
	}
}

func TestLoad_ReadsStoryDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFile)
	if err := os.WriteFile(path, []byte("story_dirs:\n  - ./a\n  - ./b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(cfg.StoryDirs, []string{"./a", "./b"}) {
		t.Fatalf("got StoryDirs %v", cfg.StoryDirs)
	}
}

func TestLoad_InterceptValidBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFile)
	body := "intercept:\n  enabled: true\n  app: ./stories/cloak/app.yaml\n  room: foyer\n  confidence_bar: 0.85\n  escape_prefix: \"!\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Intercept == nil {
		t.Fatal("expected an intercept block")
	}
	if !cfg.Intercept.Enabled {
		t.Error("expected enabled=true")
	}
	if cfg.Intercept.App != "./stories/cloak/app.yaml" {
		t.Errorf("got app %q", cfg.Intercept.App)
	}
	if cfg.Intercept.Room != "foyer" {
		t.Errorf("got room %q", cfg.Intercept.Room)
	}
	if cfg.Intercept.ConfidenceBar != 0.85 {
		t.Errorf("got confidence_bar %g, want 0.85 (an explicit bar must survive)", cfg.Intercept.ConfidenceBar)
	}
	if cfg.Intercept.EscapePrefix != "!" {
		t.Errorf("got escape_prefix %q", cfg.Intercept.EscapePrefix)
	}
}

func TestLoad_InterceptEnabledWithoutAppErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFile)
	// enabled + room but no app — must error.
	if err := os.WriteFile(path, []byte("intercept:\n  enabled: true\n  room: foyer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for an enabled intercept block with no app")
	}
}

func TestLoad_InterceptDefaultBarApplied(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFile)
	// enabled, app+room, but no confidence_bar — defaults to 0.90.
	if err := os.WriteFile(path, []byte("intercept:\n  enabled: true\n  app: a.yaml\n  room: start\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Intercept.ConfidenceBar != 0.90 {
		t.Fatalf("got confidence_bar %g, want the 0.90 default", cfg.Intercept.ConfidenceBar)
	}
}

func TestLoad_InterceptOutOfRangeBarErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFile)
	// A bar > 1 is out of (0, 1] — must error.
	if err := os.WriteFile(path, []byte("intercept:\n  enabled: true\n  app: a.yaml\n  room: start\n  confidence_bar: 1.5\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for a confidence_bar outside (0, 1]")
	}
}

func TestLoad_InterceptDisabledSkipsValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFile)
	// disabled + no app/room: validation is skipped, so this loads cleanly.
	if err := os.WriteFile(path, []byte("intercept:\n  enabled: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("disabled intercept must skip validation: %v", err)
	}
	if cfg.Intercept == nil || cfg.Intercept.Enabled {
		t.Fatal("expected a present, disabled intercept block")
	}
}

func TestLoad_InterceptLocalOverrideWinsWhole(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, DefaultConfigFile)
	local := LocalConfigPath(base)
	// Base binds room=foyer at bar 0.85; local replaces the WHOLE block.
	if err := os.WriteFile(base, []byte("intercept:\n  enabled: true\n  app: base.yaml\n  room: foyer\n  confidence_bar: 0.85\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte("intercept:\n  enabled: true\n  app: local.yaml\n  room: bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(base)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Intercept.App != "local.yaml" || cfg.Intercept.Room != "bar" {
		t.Fatalf("local block must replace the base whole: got app=%q room=%q", cfg.Intercept.App, cfg.Intercept.Room)
	}
	// The local block omits confidence_bar, so the 0.90 default applies — the
	// base's 0.85 must NOT leak through (whole-block replace, not field-merge).
	if cfg.Intercept.ConfidenceBar != 0.90 {
		t.Fatalf("got confidence_bar %g, want the 0.90 default (base 0.85 must not leak)", cfg.Intercept.ConfidenceBar)
	}
}

func TestLoad_AgentLaunchPolicyResolvesPathsAndDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFile)
	if err := os.WriteFile(path, []byte(`
agent_launch_policy:
  enabled: true
  require_capsule: true
  allowed_roots:
    - ./.worktrees/capsules
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.AgentLaunchPolicy == nil || !cfg.AgentLaunchPolicy.Enabled {
		t.Fatalf("expected enabled launch policy, got %#v", cfg.AgentLaunchPolicy)
	}
	if !cfg.AgentLaunchPolicy.RequireCapsule {
		t.Fatalf("expected require_capsule=true, got %#v", cfg.AgentLaunchPolicy)
	}
	wantProtected := filepath.Clean(dir)
	if len(cfg.AgentLaunchPolicy.ProtectedRoots) != 1 || cfg.AgentLaunchPolicy.ProtectedRoots[0] != wantProtected {
		t.Fatalf("protected_roots=%v, want [%s]", cfg.AgentLaunchPolicy.ProtectedRoots, wantProtected)
	}
	wantAllowed := filepath.Join(dir, ".worktrees", "capsules")
	if len(cfg.AgentLaunchPolicy.AllowedRoots) != 1 || cfg.AgentLaunchPolicy.AllowedRoots[0] != wantAllowed {
		t.Fatalf("allowed_roots=%v, want [%s]", cfg.AgentLaunchPolicy.AllowedRoots, wantAllowed)
	}
	if !contains(cfg.AgentLaunchPolicy.ProtectedBranches, "main") || !contains(cfg.AgentLaunchPolicy.ProtectedBranches, "integration/*") {
		t.Fatalf("expected default protected branches, got %v", cfg.AgentLaunchPolicy.ProtectedBranches)
	}
}

func TestLoad_AgentLaunchPolicyLocalOverrideWinsWhole(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, DefaultConfigFile)
	local := LocalConfigPath(base)
	if err := os.WriteFile(base, []byte(`
agent_launch_policy:
  enabled: true
  require_capsule: true
  allowed_roots:
    - ./base-allowed
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(`
agent_launch_policy:
  enabled: true
  protected_roots:
    - ./local-protected
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(base)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.AgentLaunchPolicy == nil {
		t.Fatal("expected launch policy")
	}
	if cfg.AgentLaunchPolicy.RequireCapsule {
		t.Fatalf("local policy must replace base require_capsule, got %#v", cfg.AgentLaunchPolicy)
	}
	if len(cfg.AgentLaunchPolicy.AllowedRoots) != 0 {
		t.Fatalf("local policy must replace base allowed_roots, got %v", cfg.AgentLaunchPolicy.AllowedRoots)
	}
	wantProtected := filepath.Join(dir, "local-protected")
	if len(cfg.AgentLaunchPolicy.ProtectedRoots) != 1 || cfg.AgentLaunchPolicy.ProtectedRoots[0] != wantProtected {
		t.Fatalf("protected_roots=%v, want [%s]", cfg.AgentLaunchPolicy.ProtectedRoots, wantProtected)
	}
}

func TestLoad_AgentUserDelegationResolvesPaths(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFile)
	if err := os.WriteFile(path, []byte(`
agent_user_delegation:
  enabled: true
  run_as_user: kitsoki-agent
  wrapper_bin: ./agent-bin
  capsule_root: ./capsules
  receipt_path: ./.artifacts/run-as-user/receipt.json
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.AgentUserDelegation == nil || !cfg.AgentUserDelegation.Enabled {
		t.Fatalf("expected enabled user delegation, got %#v", cfg.AgentUserDelegation)
	}
	if cfg.AgentUserDelegation.RunAsUser != "kitsoki-agent" {
		t.Fatalf("run_as_user=%q", cfg.AgentUserDelegation.RunAsUser)
	}
	if got, want := cfg.AgentUserDelegation.WrapperBin, filepath.Join(dir, "agent-bin"); got != want {
		t.Fatalf("wrapper_bin=%q, want %q", got, want)
	}
	if got, want := cfg.AgentUserDelegation.CapsuleRoot, filepath.Join(dir, "capsules"); got != want {
		t.Fatalf("capsule_root=%q, want %q", got, want)
	}
	if got, want := cfg.AgentUserDelegation.ReceiptPath, filepath.Join(dir, ".artifacts", "run-as-user", "receipt.json"); got != want {
		t.Fatalf("receipt_path=%q, want %q", got, want)
	}
}

func TestLoad_AgentUserDelegationAllowsEnabledWithoutUserWhileDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, DefaultConfigFile)
	if err := os.WriteFile(path, []byte(`
agent_user_delegation:
  enabled: true
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("disabled run_as_user should keep config loadable: %v", err)
	}
	if cfg.AgentUserDelegation == nil || !cfg.AgentUserDelegation.Enabled {
		t.Fatalf("expected enabled user delegation block to load, got %#v", cfg.AgentUserDelegation)
	}
	if cfg.AgentUserDelegation.RunAsUser != "" {
		t.Fatalf("run_as_user=%q, want empty", cfg.AgentUserDelegation.RunAsUser)
	}
}

func TestLoad_AgentUserDelegationLocalOverrideWinsWhole(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, DefaultConfigFile)
	local := LocalConfigPath(base)
	if err := os.WriteFile(base, []byte(`
agent_user_delegation:
  enabled: true
  run_as_user: base-agent
  wrapper_bin: ./base-bin
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(local, []byte(`
agent_user_delegation:
  enabled: true
  run_as_user: local-agent
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(base)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.AgentUserDelegation == nil {
		t.Fatal("expected user delegation config")
	}
	if cfg.AgentUserDelegation.RunAsUser != "local-agent" {
		t.Fatalf("run_as_user=%q", cfg.AgentUserDelegation.RunAsUser)
	}
	if cfg.AgentUserDelegation.WrapperBin != "" {
		t.Fatalf("local block must replace base wrapper_bin, got %q", cfg.AgentUserDelegation.WrapperBin)
	}
}

func TestResolve_Precedence(t *testing.T) {
	tests := []struct {
		name     string
		flagDirs []string
		cfg      WebConfig
		want     []string
	}{
		{
			name:     "flags win over config and default",
			flagDirs: []string{"/flag/one", "/flag/two"},
			cfg:      WebConfig{StoryDirs: []string{"/cfg"}},
			want:     []string{"/flag/one", "/flag/two"},
		},
		{
			name: "config wins over default when no flags",
			cfg:  WebConfig{StoryDirs: []string{"/cfg"}},
			want: []string{"/cfg"},
		},
		{
			name: "default when neither flags nor config",
			want: []string{"./stories"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Resolve(tc.flagDirs, tc.cfg)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Resolve = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolve_ReturnsCopy(t *testing.T) {
	// The default path must not hand out the package-level slice.
	got := Resolve(nil, WebConfig{})
	got[0] = "mutated"
	again := Resolve(nil, WebConfig{})
	if again[0] != "./stories" {
		t.Fatalf("Resolve leaked the default slice: got %v", again)
	}
}

func TestDiscoverStories_NestedValidAndMalformed(t *testing.T) {
	root := t.TempDir()

	// Two valid stories at different nesting depths.
	absAlpha := writeStory(t, filepath.Join(root, "alpha"), "alpha", "Alpha")
	absBeta := writeStory(t, filepath.Join(root, "nested", "beta"), "beta", "Beta")

	// One malformed story (root state references a nonexistent state) — it must
	// be skipped without aborting the walk or hiding its valid siblings.
	badDir := filepath.Join(root, "broken")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "app.yaml"), []byte(malformedStory), 0o644); err != nil {
		t.Fatal(err)
	}

	// A non-app.yaml file must be ignored.
	if err := os.WriteFile(filepath.Join(root, "alpha", "notes.yaml"), []byte("foo: bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	metas, err := DiscoverStories([]string{root}, nil)
	if err != nil {
		t.Fatalf("DiscoverStories: %v", err)
	}

	gotPaths := make([]string, 0, len(metas))
	for _, m := range metas {
		gotPaths = append(gotPaths, m.Path)
		if !filepath.IsAbs(m.Path) {
			t.Errorf("Path %q is not absolute", m.Path)
		}
		if m.Def == nil {
			t.Errorf("StoryMeta for %s has nil Def", m.Path)
		}
	}
	sort.Strings(gotPaths)

	want := []string{absAlpha, absBeta}
	sort.Strings(want)
	if !reflect.DeepEqual(gotPaths, want) {
		t.Fatalf("discovered %v, want %v (malformed must be skipped)", gotPaths, want)
	}

	// Spot-check the loaded def is the real thing, not a placeholder.
	for _, m := range metas {
		if m.Def.App.ID == "" {
			t.Errorf("loaded Def for %s has empty App.ID", m.Path)
		}
	}
}

func TestDiscoverStories_UnreadableRootIsError(t *testing.T) {
	_, err := DiscoverStories([]string{filepath.Join(t.TempDir(), "does-not-exist")}, nil)
	if err == nil {
		t.Fatal("expected an error for a nonexistent root dir")
	}
}
