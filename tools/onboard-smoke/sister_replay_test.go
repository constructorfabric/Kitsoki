//go:build onboardsisters

package onboardsmoke

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/projectprofile"
)

const sisterCommandTimeout = 2 * time.Minute

type sisterCase struct {
	name              string
	sourceEnv         string
	sourceDefault     string
	baselineCommit    string
	deterministicFlow string
	projectID         string
	projectTitle      string
	stack             string
	devCommand        string
	testCommand       string
	buildCommand      string
	wantProfile       []string
}

func TestOnboardSisterProjects_FromBaseline(t *testing.T) {
	requireTool(t, "git")
	requireTool(t, "python3")

	kitsokiRoot := repoRoot(t)
	cases := []sisterCase{
		{
			name:              "gears-rust",
			sourceEnv:         "KITSOKI_GEARS_RUST_REPO",
			sourceDefault:     filepath.Join(filepath.Dir(kitsokiRoot), "gears-rust"),
			baselineCommit:    "d8513b0c4e7f12c0e451fde5253eafa0cc38d6a5",
			deterministicFlow: "stories/dev-story/flows/init_rust_project.yaml",
			projectID:         "gears-rust",
			projectTitle:      "Gears Rust",
			stack:             "rust project",
			devCommand:        "make dev",
			testCommand:       "make test",
			buildCommand:      "make build",
			wantProfile: []string{
				`id: gears-rust`,
				`test: "make test"`,
				`build: "make build"`,
				`check: "make check"`,
				`path: .kitsoki/stories/gears-rust-dev/app.yaml`,
				`monorepo: true`,
			},
		},
		{
			name:              "slidey",
			sourceEnv:         "KITSOKI_SLIDEY_REPO",
			sourceDefault:     filepath.Join(filepath.Dir(kitsokiRoot), "slidey"),
			baselineCommit:    "1a018e5939ee662d37c392067ce496d9c94d1b68",
			deterministicFlow: "stories/dev-story/flows/init_slidey_dogfood.yaml",
			projectID:         "slidey",
			projectTitle:      "Slidey",
			stack:             "node/vue/vite/puppeteer declarative deck engine with web, html, pdf, and mp4 outputs",
			devCommand:        "node src/index.js examples/hello.slidey.json --port 5000 --no-open",
			testCommand:       "npm test",
			buildCommand:      "npm run build",
			wantProfile: []string{
				`id: slidey`,
				`build: "npm run build"`,
				`test: "npm test"`,
				`path: ".kitsoki/stories/slidey-dev/app.yaml"`,
				`command: "kitsoki run .kitsoki/stories/slidey-dev/app.yaml"`,
				`html_bundle: "node src/index.js bundle examples/hello.slidey.json .artifacts/hello.html"`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runSisterReplay(t, kitsokiRoot, tc)
		})
	}
}

func runSisterReplay(t *testing.T, kitsokiRoot string, tc sisterCase) {
	t.Helper()

	source := os.Getenv(tc.sourceEnv)
	if source == "" {
		source = tc.sourceDefault
	}
	if info, err := os.Stat(filepath.Join(source, ".git")); err != nil || !info.IsDir() {
		t.Skipf("%s checkout not found at %s; set %s to run this replay", tc.name, source, tc.sourceEnv)
	}
	assertFile(t, source, filepath.Join(".kitsoki", "project-profile.yaml"),
		"baseline_commit: "+tc.baselineCommit,
		"deterministic_flow: "+tc.deterministicFlow,
		"recording_policy: gated-live-allowed",
	)
	sisterRun(t, source, "git", "cat-file", "-e", tc.baselineCommit+"^{commit}")

	work := filepath.Join(t.TempDir(), tc.projectID)
	sisterRun(t, "", "git", "clone", "-q", "--no-checkout", source, work)
	sisterRun(t, work, "git", "checkout", "-q", tc.baselineCommit)
	assertPreInitClean(t, work, tc.projectID)

	out := runOutput(t, work, "python3",
		filepath.Join(kitsokiRoot, "stories", "dev-story", "scripts", "init_apply.py"),
		work,
		tc.projectID,
		tc.projectTitle,
		tc.stack,
		tc.devCommand,
		tc.testCommand,
		tc.buildCommand,
		"hybrid",
		"none",
	)
	var applied struct {
		Status       string   `json:"status"`
		ConfigPath   string   `json:"config_path"`
		ProfilePath  string   `json:"profile_path"`
		InstancePath string   `json:"instance_path"`
		Gitignore    string   `json:"gitignore_path"`
		Writes       []string `json:"writes"`
	}
	if err := json.Unmarshal([]byte(out), &applied); err != nil {
		t.Fatalf("parse init_apply output:\n%s\n%v", out, err)
	}
	if applied.Status != "applied" {
		t.Fatalf("init_apply status = %q, want applied", applied.Status)
	}

	instanceRel := filepath.Join(".kitsoki", "stories", tc.projectID+"-dev", "app.yaml")
	assertFile(t, work, ".kitsoki.yaml", "story_dirs:\n  - ./.kitsoki/stories", "default_story: "+filepath.ToSlash(instanceRel))
	assertFile(t, work, ".gitignore", ".kitsoki.local.yaml", ".kitsoki/sessions/", ".context", ".artifacts", ".worktrees")
	assertFile(t, work, instanceRel, `source: "@kitsoki/dev-story"`, "root: core")
	assertFile(t, work, filepath.Join(".kitsoki", "stories", tc.projectID+"-dev", "README.md"), "kitsoki run "+filepath.ToSlash(instanceRel))
	assertFile(t, work, filepath.Join(".kitsoki", "project-profile.yaml"), tc.wantProfile...)
	assertProjectProfileValid(t, work, filepath.Join(".kitsoki", "project-profile.yaml"))

	if _, err := os.Stat(filepath.Join(work, "stories", tc.projectID+"-dev", "app.yaml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("root stories/%s-dev/app.yaml should not exist after onboarding; err=%v", tc.projectID, err)
	}
	assertGeneratedAppLoads(t, kitsokiRoot, filepath.Join(work, instanceRel))

	status := runOutput(t, work, "git", "status", "--short")
	for _, forbidden := range []string{
		"?? stories/",
		"?? " + filepath.ToSlash(filepath.Join("stories", tc.projectID+"-dev")),
	} {
		if strings.Contains(status, forbidden) {
			t.Fatalf("onboarding polluted root stories directory:\n%s", status)
		}
	}
}

func assertProjectProfileValid(t *testing.T, root, rel string) {
	t.Helper()
	res, err := projectprofile.ValidateFile(filepath.Join(root, rel), root)
	if err != nil {
		t.Fatalf("validate %s: %v", rel, err)
	}
	if !res.OK {
		t.Fatalf("%s failed project-profile validation: schema=%v semantic=%v warnings=%v", rel, res.Schema, res.Semantic, res.Warnings)
	}
}

func assertPreInitClean(t *testing.T, root, projectID string) {
	t.Helper()
	for _, rel := range []string{
		".kitsoki.yaml",
		filepath.Join(".kitsoki", "project-profile.yaml"),
		filepath.Join(".kitsoki", "stories", projectID+"-dev", "app.yaml"),
		filepath.Join("stories", projectID+"-dev", "app.yaml"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("baseline already has %s; err=%v", rel, err)
		}
	}
}

func assertGeneratedAppLoads(t *testing.T, kitsokiRoot, appPath string) {
	t.Helper()
	resolver := app.ImportResolver(func(name, _ string, _ bool) (string, error) {
		candidate := filepath.Join(kitsokiRoot, "stories", name, "app.yaml")
		if _, err := os.Stat(candidate); err != nil {
			return "", nil
		}
		return candidate, nil
	})
	if _, err := app.LoadWithResolver(appPath, nil, resolver); err != nil {
		t.Fatalf("generated app does not load: %v", err)
	}
}

func assertFile(t *testing.T, root, rel string, wantSubstrings ...string) {
	t.Helper()
	path := filepath.Join(root, rel)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	text := string(b)
	for _, want := range wantSubstrings {
		if !strings.Contains(text, want) {
			t.Fatalf("%s missing %q\n--- file ---\n%s", rel, want, text)
		}
	}
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not on PATH", name)
	}
}

func sisterRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	_ = runOutput(t, dir, name, args...)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve kitsoki repo root: %v", err)
	}
	return root
}

func runOutput(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), sisterCommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s %v timed out: %v\n%s", name, args, ctx.Err(), out)
	}
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}
