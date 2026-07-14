package pogbootstrap

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/graph"
	"kitsoki/internal/kit"
)

const kitsokiTestBinaryEnv = "KITSOKI_TEST_KITSOKI_BINARY"

var buildKitsokiBinaryOnce sync.Once
var builtKitsokiBinary string
var builtKitsokiBinaryDir string
var buildKitsokiBinaryErr error

func TestMain(m *testing.M) {
	code := m.Run()
	if builtKitsokiBinaryDir != "" {
		_ = os.RemoveAll(builtKitsokiBinaryDir)
	}
	os.Exit(code)
}

func writeBrief(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "brief.md")
	body := `# Acme Notes

Tiny product for field notes.

## Requirements

- Capture a note locally
- List previous notes

## Use cases

- Operator records a note from the command line
- Operator reviews notes before filing a report

## Unknowns

- Which license should this use?
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write brief: %v", err)
	}
	return path
}

func TestEmbeddedPOGKitLoads(t *testing.T) {
	dir := materializeEmbeddedKit(t)
	manifest, err := kit.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if manifest.Identity() != "@kitsoki/pog" || !manifest.HasStory("init") {
		t.Fatalf("unexpected manifest identity/stories: %s %v", manifest.Identity(), manifest.Provides.Stories)
	}
	if _, err := app.Load(filepath.Join(dir, "stories", "init", "app.yaml")); err != nil {
		t.Fatalf("embedded init story did not load: %v", err)
	}
}

func TestRunDryRunDoesNotMutate(t *testing.T) {
	tmp := t.TempDir()
	brief := writeBrief(t, tmp)
	repo := filepath.Join(tmp, "repo")
	var out bytes.Buffer
	res, err := Run(Options{
		RepoPath:       repo,
		BriefPath:      brief,
		DryRun:         true,
		KitsokiCommand: "kitsoki",
	}, &out)
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatalf("dry-run created repo path; stat err=%v", err)
	}
	if len(res.Plan) == 0 {
		t.Fatal("dry-run returned empty plan")
	}
	for _, want := range []string{"git init -b main", "pog/catalog.yaml", "remote none", "scripts/checks.sh", "scripts/merge-to-main.sh", "scripts/sync-main-from-remote.sh"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunInitializesLintCleanRepo(t *testing.T) {
	tmp := t.TempDir()
	brief := writeBrief(t, tmp)
	repo := filepath.Join(tmp, "repo")
	kitsokiCommand := kitsokiCommandForTest(t)

	var out bytes.Buffer
	res, err := Run(Options{
		RepoPath:       repo,
		BriefPath:      brief,
		Yes:            true,
		KitsokiCommand: kitsokiCommand,
	}, &out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out.String())
	}

	if _, err := graph.LoadCatalog(filepath.Join(repo, "pog", "catalog.yaml")); err != nil {
		t.Fatalf("generated catalog did not load: %v", err)
	}
	cat, err := graph.LoadCatalog(filepath.Join(repo, "pog", "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if issues := graph.Lint(cat); len(issues) > 0 {
		t.Fatalf("generated catalog lint issues: %v", issues)
	}
	decisionCount := 0
	for _, node := range cat.Nodes {
		if node.TypeID == "decision" && node.Status == "pending" {
			decisionCount++
		}
	}
	if decisionCount < 4 {
		t.Fatalf("expected brief unknowns plus default pending decision nodes, got %d", decisionCount)
	}
	for _, path := range []string{"AGENTS.md", "docs/onboarding.md", "scripts/checks.sh", "scripts/pog-doctor", "scripts/merge-to-main.sh", "scripts/land-branch.sh", "scripts/sync-main-from-remote.sh"} {
		if _, err := os.Stat(filepath.Join(repo, path)); err != nil {
			t.Fatalf("missing %s: %v", path, err)
		}
	}
	agents, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(agents), "pack:conventions:begin") {
		t.Fatal("AGENTS.md missing conventions block")
	}
	hook := filepath.Join(repo, ".git", "hooks", "reference-transaction")
	if info, err := os.Stat(hook); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("protected-main hook not executable; info=%v err=%v", info, err)
	}
	if remotes, err := exec.Command("git", "-C", repo, "remote").CombinedOutput(); err != nil || strings.TrimSpace(string(remotes)) != "" {
		t.Fatalf("unexpected remote state err=%v out=%s", err, remotes)
	}
	check := exec.Command("scripts/checks.sh")
	check.Dir = repo
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("generated checks.sh failed: %v\n%s", err, output)
	}
	rooms, err := TraceRooms(res.TracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	for _, want := range []string{"brief.intake", "repo.plan", "operator.consent", "repo.apply", "repo.verify", "handoff"} {
		if !contains(rooms, want) {
			t.Fatalf("trace missing room %q; rooms=%v", want, rooms)
		}
	}
	events := readTraceEvents(t, res.TracePath)
	if !traceHasEvent(events, "operator.consent", "decisions") {
		t.Fatalf("trace missing operator decision evidence: %#v", events)
	}
	if !traceHasEvent(events, "repo.apply", "applied") {
		t.Fatalf("trace missing apply evidence: %#v", events)
	}
	if !traceHasEvent(events, "repo.verify", "verification") {
		t.Fatalf("trace missing verification evidence: %#v", events)
	}
	if !strings.Contains(out.String(), "next: open docs/onboarding.md") {
		t.Fatalf("stdout missing handoff next action:\n%s", out.String())
	}
}

func TestRunCanCreateInitialCommit(t *testing.T) {
	tmp := t.TempDir()
	brief := writeBrief(t, tmp)
	repo := filepath.Join(tmp, "repo")
	kitsokiCommand := kitsokiCommandForTest(t)

	var out bytes.Buffer
	res, err := Run(Options{
		RepoPath:       repo,
		BriefPath:      brief,
		Yes:            true,
		Commit:         true,
		KitsokiCommand: kitsokiCommand,
	}, &out)
	if err != nil {
		t.Fatalf("run --commit: %v\n%s", err, out.String())
	}
	if res.CommitID == "" {
		t.Fatal("expected commit id in result")
	}
	if output, err := exec.Command("git", "-C", repo, "status", "--short").CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("expected clean generated repo after commit, err=%v output=%s", err, output)
	}
}

func TestRunRejectsRemoteWithoutConsent(t *testing.T) {
	tmp := t.TempDir()
	brief := writeBrief(t, tmp)
	var out bytes.Buffer
	_, err := Run(Options{
		RepoPath:       filepath.Join(tmp, "repo"),
		BriefPath:      brief,
		Yes:            true,
		Remote:         "github:owner/name",
		KitsokiCommand: "kitsoki",
	}, &out)
	if err == nil || !strings.Contains(err.Error(), "requires an interactive operator consent") {
		t.Fatalf("expected remote consent error, got %v", err)
	}
}

func TestRunDryRunShowsRemoteWithoutConsentFailure(t *testing.T) {
	tmp := t.TempDir()
	brief := writeBrief(t, tmp)
	repo := filepath.Join(tmp, "repo")
	var out bytes.Buffer
	_, err := Run(Options{
		RepoPath:       repo,
		BriefPath:      brief,
		DryRun:         true,
		Remote:         "github:owner/name",
		KitsokiCommand: "kitsoki",
	}, &out)
	if err != nil {
		t.Fatalf("remote dry-run should not require consent: %v", err)
	}
	if _, err := os.Stat(repo); !os.IsNotExist(err) {
		t.Fatalf("remote dry-run created repo path; stat err=%v", err)
	}
	if !strings.Contains(out.String(), "github:owner/name") || !strings.Contains(out.String(), "consent required") {
		t.Fatalf("remote dry-run did not show consent-gated remote action:\n%s", out.String())
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
			return wd
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			t.Fatal("could not find repo root")
		}
		wd = parent
	}
}

func kitsokiCommandForTest(t *testing.T) string {
	t.Helper()
	if path := os.Getenv(kitsokiTestBinaryEnv); path != "" {
		if info, err := os.Stat(path); err != nil {
			t.Fatalf("%s=%q is not usable: %v", kitsokiTestBinaryEnv, path, err)
		} else if info.IsDir() || info.Mode()&0o111 == 0 {
			t.Fatalf("%s=%q is not an executable file", kitsokiTestBinaryEnv, path)
		}
		return path
	}

	buildKitsokiBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "kitsoki-pogbootstrap-*")
		if err != nil {
			buildKitsokiBinaryErr = err
			return
		}
		builtKitsokiBinaryDir = dir
		builtKitsokiBinary = filepath.Join(dir, "kitsoki")
		build := exec.Command("go", "build", "-o", builtKitsokiBinary, "./cmd/kitsoki")
		build.Dir = repoRoot(t)
		if output, err := build.CombinedOutput(); err != nil {
			buildKitsokiBinaryErr = &buildError{err: err, output: output}
		}
	})
	if buildKitsokiBinaryErr != nil {
		t.Fatalf("build kitsoki fixture binary: %v", buildKitsokiBinaryErr)
	}
	return builtKitsokiBinary
}

type buildError struct {
	err    error
	output []byte
}

func (e *buildError) Error() string {
	return e.err.Error() + "\n" + string(e.output)
}

func readTraceEvents(t *testing.T, path string) []traceEvent {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var events []traceEvent
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var ev traceEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("parse trace line %q: %v", line, err)
		}
		events = append(events, ev)
	}
	return events
}

func traceHasEvent(events []traceEvent, room, event string) bool {
	for _, ev := range events {
		if ev.Room == room && ev.Event == event {
			return true
		}
	}
	return false
}

func materializeEmbeddedKit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range []string{"kit.yaml", filepath.Join("stories", "init", "app.yaml")} {
		raw, err := kitFS.ReadFile(filepath.ToSlash(filepath.Join("kit", rel)))
		if err != nil {
			t.Fatalf("read embedded kit file %s: %v", rel, err)
		}
		out := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
