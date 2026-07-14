package starlark_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goyaml "github.com/goccy/go-yaml"
	starlarkhost "kitsoki/internal/host/starlark"
)

// runWith evaluates src with the given inspector injected and returns the
// outputs (or fatals on error). It is the inspection-side analogue of the HTTP
// tests' run helpers.
func runInspect(t *testing.T, in starlarkhost.Inspector, src string) map[string]any {
	t.Helper()
	ctx := context.Background()
	if in != nil {
		ctx = starlarkhost.WithInspector(ctx, in)
	}
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "<test>",
		Source:       []byte(src),
		World:        map[string]any{},
		Inputs:       map[string]any{},
		Capabilities: inspectCaps(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res.Outputs
}

// runErr evaluates src and returns the (domain) error message, fataling if the
// run unexpectedly succeeds.
func runErr(t *testing.T, in starlarkhost.Inspector, src string) string {
	t.Helper()
	ctx := context.Background()
	if in != nil {
		ctx = starlarkhost.WithInspector(ctx, in)
	}
	_, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "<test>",
		Source:       []byte(src),
		World:        map[string]any{},
		Inputs:       map[string]any{},
		Capabilities: inspectCaps(),
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if msg, ok := starlarkhost.AsDomainError(err); ok {
		return msg
	}
	return err.Error()
}

func inspectCaps() starlarkhost.CapabilitySpec {
	return starlarkhost.CapabilitySpec{
		World: true,
		FS: starlarkhost.FSCapability{
			ReadPatterns:  []string{"**"},
			WritePatterns: []string{"**"},
		},
		Probe: starlarkhost.ProbeCapability{Names: []string{"git.status", "git.ls_files", "gh.issue.list", "rm.rf"}},
	}
}

func runInspectErrWithCaps(t *testing.T, in starlarkhost.Inspector, src string, caps starlarkhost.CapabilitySpec) string {
	t.Helper()
	ctx := context.Background()
	if in != nil {
		ctx = starlarkhost.WithInspector(ctx, in)
	}
	_, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "<test>",
		Source:       []byte(src),
		World:        map[string]any{},
		Inputs:       map[string]any{},
		Capabilities: caps,
	})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if msg, ok := starlarkhost.AsDomainError(err); ok {
		return msg
	}
	return err.Error()
}

// ─── production inspector: rooted fs ─────────────────────────────────────────

func TestProductionInspector_ReadExistsGlob(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := starlarkhost.NewProductionInspector(root)

	out := runInspect(t, in, `
def main(ctx):
    return {
        "body": ctx.fs.read("go.mod"),
        "has": ctx.fs.exists("go.mod"),
        "absent": ctx.fs.exists("nope.txt"),
        "matches": ctx.fs.glob("sub/*.txt"),
    }
`)
	if out["body"] != "module x\n" {
		t.Errorf("read: got %q", out["body"])
	}
	if out["has"] != true {
		t.Errorf("exists(go.mod): got %v", out["has"])
	}
	if out["absent"] != false {
		t.Errorf("exists(nope): got %v", out["absent"])
	}
	matches, _ := out["matches"].([]any)
	if len(matches) != 1 || matches[0] != "sub/a.txt" {
		t.Errorf("glob: got %v", out["matches"])
	}
}

func TestProductionInspector_Write(t *testing.T) {
	root := t.TempDir()
	in := starlarkhost.NewProductionInspector(root)

	out := runInspect(t, in, `
def main(ctx):
    path = ctx.fs.write(".artifacts/demo/report.md", "hello\n")
    return {
        "path": path,
        "body": ctx.fs.read(path),
    }
`)
	if out["path"] != ".artifacts/demo/report.md" {
		t.Errorf("write path: got %q", out["path"])
	}
	if out["body"] != "hello\n" {
		t.Errorf("read after write: got %q", out["body"])
	}
	if got, err := os.ReadFile(filepath.Join(root, ".artifacts", "demo", "report.md")); err != nil || string(got) != "hello\n" {
		t.Fatalf("file on disk = %q, %v", string(got), err)
	}
}

func TestCapabilityPolicy_DeniesUnmatchedFSWrite(t *testing.T) {
	root := t.TempDir()
	in := starlarkhost.NewProductionInspector(root)
	msg := runInspectErrWithCaps(t, in, `
def main(ctx):
    return {"path": ctx.fs.write("docs/out.md", "nope")}
`, starlarkhost.CapabilitySpec{
		World: true,
		FS: starlarkhost.FSCapability{
			ReadPatterns:  []string{"**"},
			WritePatterns: []string{".artifacts/**"},
		},
	})
	if !strings.Contains(msg, "not granted") {
		t.Fatalf("expected path grant error, got %q", msg)
	}
}

func TestProductionInspector_TempAbsolutePath(t *testing.T) {
	root := t.TempDir()
	outPath := filepath.Join(os.TempDir(), "kitsoki-starlark-inspect-test", "report.md")
	t.Cleanup(func() { _ = os.Remove(outPath) })
	in := starlarkhost.NewProductionInspector(root)

	out := runInspect(t, in, fmt.Sprintf(`
def main(ctx):
    path = ctx.fs.write(%q, "hello\n")
    return {
        "path": path,
        "body": ctx.fs.read(path),
    }
`, outPath))
	if out["path"] != outPath {
		t.Errorf("write path: got %q", out["path"])
	}
	if out["body"] != "hello\n" {
		t.Errorf("read after write: got %q", out["body"])
	}

	msg := runErr(t, in, `def main(ctx):
    return {"v": ctx.fs.exists("/etc/passwd")}
`)
	if !strings.Contains(msg, "repo-relative or under /tmp") {
		t.Errorf("expected absolute path boundary error, got %q", msg)
	}
}

func TestProductionInspector_RejectsEscape(t *testing.T) {
	root := t.TempDir()
	in := starlarkhost.NewProductionInspector(root)

	for _, expr := range []string{
		`ctx.fs.read("../secret")`,
		`ctx.fs.read("../../etc/passwd")`,
		`ctx.fs.exists("sub/../../x")`,
		`ctx.fs.glob("../*")`,
		`ctx.fs.write("../secret", "x")`,
	} {
		msg := runErr(t, in, "def main(ctx):\n    return {\"v\": "+expr+"}\n")
		if !strings.Contains(msg, "escapes the working directory") {
			t.Errorf("%s: expected escape error, got %q", expr, msg)
		}
	}
}

func TestProductionInspector_ReadSizeCap(t *testing.T) {
	root := t.TempDir()
	big := make([]byte, (1<<20)+1)
	if err := os.WriteFile(filepath.Join(root, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	in := starlarkhost.NewProductionInspector(root)
	msg := runErr(t, in, `
def main(ctx):
    return {"v": ctx.fs.read("big.bin")}
`)
	if !strings.Contains(msg, "byte cap") {
		t.Errorf("expected size-cap error, got %q", msg)
	}
}

// ─── production inspector: probe allow-list ──────────────────────────────────

func TestProbe_RejectsUnknownName(t *testing.T) {
	in := starlarkhost.NewProductionInspector(t.TempDir())
	msg := runErr(t, in, `
def main(ctx):
    return {"v": ctx.probe("rm.rf", ["/"])}
`)
	if !strings.Contains(msg, "not on the allow-list") {
		t.Errorf("expected allow-list rejection, got %q", msg)
	}
}

func TestProbe_AllowListedGitStatus(t *testing.T) {
	// git.status is read-only and safe to run in a temp git repo.
	root := t.TempDir()
	in := starlarkhost.NewProductionInspector(root)
	// A non-git dir makes `git status` exit non-zero; the probe must surface that
	// exit code as a result, not an error.
	out := runInspect(t, in, `
def main(ctx):
    r = ctx.probe("git.status")
    return {"exit": r["exit"]}
`)
	// Exit is an int64 after conversion; just assert it is present and an integer.
	if _, ok := out["exit"].(int64); !ok {
		t.Errorf("probe exit: got %T %v", out["exit"], out["exit"])
	}
}

func TestCapabilityPolicy_DeniesUnmatchedProbe(t *testing.T) {
	in := starlarkhost.NewReplayInspector(&starlarkhost.InspectCassette{
		Kind: "inspect_cassette",
		Interactions: []starlarkhost.InspectInteraction{
			{Op: "probe", Target: "gh.issue.list", Exit: 0, Out: "[]"},
		},
	})
	msg := runInspectErrWithCaps(t, in, `
def main(ctx):
    return {"out": ctx.probe("gh.issue.list", ["owner/repo"])["out"]}
`, starlarkhost.CapabilitySpec{
		World: true,
		Probe: starlarkhost.ProbeCapability{
			Names: []string{"git.status"},
		},
	})
	if !strings.Contains(msg, "probe") || !strings.Contains(msg, "not granted") {
		t.Fatalf("expected probe grant error, got %q", msg)
	}
}

func TestProbe_GHIssueListUsesNativeGitHubAPI(t *testing.T) {
	t.Setenv("GH_TOKEN", "test-token")
	t.Setenv("PATH", t.TempDir())

	var sawRequest bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodGet || r.URL.Path != "/search/issues" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("Authorization = %q", got)
		}
		q := r.URL.Query().Get("q")
		if !strings.Contains(q, "repo:owner/repo") || !strings.Contains(q, "is:issue") {
			t.Fatalf("query = %q", q)
		}
		if got := r.URL.Query().Get("per_page"); got != "200" {
			t.Fatalf("per_page = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{
			"number": 7,
			"title":  "Native issue",
			"state":  "open",
		}}}); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()
	restoreAPI := starlarkhost.SetGitHubProbeAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	in := starlarkhost.NewProductionInspector(t.TempDir())
	res, err := in.Probe(context.Background(), "gh.issue.list", []string{"owner/repo"})
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !sawRequest {
		t.Fatal("expected native GitHub API request")
	}
	if res.Exit != 0 {
		t.Fatalf("Exit = %d, out = %q", res.Exit, res.Out)
	}
	if !strings.Contains(res.Out, `"number":7`) || !strings.Contains(res.Out, `"title":"Native issue"`) {
		t.Fatalf("Out = %q", res.Out)
	}
}

// ─── deny-all default ────────────────────────────────────────────────────────

func TestDeniedInspector_Default(t *testing.T) {
	// No inspector injected → every fs/probe call fails loud.
	for _, expr := range []string{
		`ctx.fs.read("go.mod")`,
		`ctx.fs.exists("go.mod")`,
		`ctx.fs.glob("*.go")`,
		`ctx.fs.write("out.txt", "x")`,
		`ctx.probe("git.status")`,
	} {
		msg := runErr(t, nil, "def main(ctx):\n    return {\"v\": "+expr+"}\n")
		if !strings.Contains(msg, "no inspector injected") {
			t.Errorf("%s: expected deny-all error, got %q", expr, msg)
		}
	}
}

// ─── replay inspector ────────────────────────────────────────────────────────

func TestReplayInspector_ServesRecorded(t *testing.T) {
	cas := &starlarkhost.InspectCassette{
		Kind: "inspect_cassette",
		Interactions: []starlarkhost.InspectInteraction{
			{Op: "read", Target: "go.mod", Out: "module kitsoki\n"},
			{Op: "exists", Target: "MISSING", Out: "false"},
			{Op: "glob", Target: "*.go", Out: "a.go\nb.go"},
			{Op: "write", Target: ".artifacts/out.txt", Out: "ok\n"},
			{Op: "probe", Target: "gh.issue.list", Exit: 0, Out: `[{"number":1}]`},
		},
	}
	in := starlarkhost.NewReplayInspector(cas)
	out := runInspect(t, in, `
def main(ctx):
    r = ctx.probe("gh.issue.list", ["owner/repo"])
    return {
        "body": ctx.fs.read("go.mod"),
        "absent": ctx.fs.exists("MISSING"),
        "matches": ctx.fs.glob("*.go"),
        "written": ctx.fs.write(".artifacts/out.txt", "ok\n"),
        "probe_exit": r["exit"],
        "probe_out": r["out"],
    }
`)
	if out["body"] != "module kitsoki\n" {
		t.Errorf("read: got %q", out["body"])
	}
	if out["absent"] != false {
		t.Errorf("exists: got %v", out["absent"])
	}
	matches, _ := out["matches"].([]any)
	if len(matches) != 2 || matches[0] != "a.go" {
		t.Errorf("glob: got %v", out["matches"])
	}
	if out["written"] != ".artifacts/out.txt" {
		t.Errorf("write replay: got %v", out["written"])
	}
}

func TestReplayInspector_MissFailsLoud(t *testing.T) {
	cas := &starlarkhost.InspectCassette{
		Kind:         "inspect_cassette",
		Interactions: []starlarkhost.InspectInteraction{{Op: "read", Target: "go.mod", Out: "x"}},
	}
	in := starlarkhost.NewReplayInspector(cas)
	msg := runErr(t, in, `
def main(ctx):
    return {"v": ctx.fs.read("other.txt")}
`)
	if !strings.Contains(msg, "no interaction matched") {
		t.Errorf("expected replay miss, got %q", msg)
	}
}

func TestReplayInspector_WriteMismatchFailsLoud(t *testing.T) {
	cas := &starlarkhost.InspectCassette{
		Kind:         "inspect_cassette",
		Interactions: []starlarkhost.InspectInteraction{{Op: "write", Target: "out.txt", Out: "expected"}},
	}
	in := starlarkhost.NewReplayInspector(cas)
	msg := runErr(t, in, `
def main(ctx):
    return {"v": ctx.fs.write("out.txt", "actual")}
`)
	if !strings.Contains(msg, "content mismatch") {
		t.Errorf("expected replay write mismatch, got %q", msg)
	}
}

func TestReplayInspector_Summaries(t *testing.T) {
	cas := &starlarkhost.InspectCassette{
		Kind: "inspect_cassette",
		Interactions: []starlarkhost.InspectInteraction{
			{Op: "probe", Target: "git.status", Exit: 1, Out: "fatal"},
		},
	}
	in := starlarkhost.NewReplayInspector(cas)
	ctx := starlarkhost.WithInspector(context.Background(), in)
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "<test>",
		Source:       []byte("def main(ctx):\n    return {\"e\": ctx.probe(\"git.status\")[\"exit\"]}\n"),
		World:        map[string]any{},
		Inputs:       map[string]any{},
		Capabilities: inspectCaps(),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Inspections) != 1 {
		t.Fatalf("expected 1 inspection summary, got %d", len(res.Inspections))
	}
	ix := res.Inspections[0]
	if ix.Op != "probe" || ix.Target != "git.status" || ix.Status != "exit:1" {
		t.Errorf("summary: got %+v", ix)
	}
}

// ─── cassette round-trip ─────────────────────────────────────────────────────

func TestInspectCassette_RoundTrip(t *testing.T) {
	cas := &starlarkhost.InspectCassette{
		Kind: "inspect_cassette",
		Interactions: []starlarkhost.InspectInteraction{
			{Op: "read", Target: "go.mod", Out: "module kitsoki\n"},
			{Op: "probe", Target: "gh.issue.list", Exit: 0, Out: "[]"},
		},
	}
	data, err := goyaml.Marshal(cas)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back starlarkhost.InspectCassette
	if err := goyaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	in := starlarkhost.NewReplayInspector(&back)
	out := runInspect(t, in, `
def main(ctx):
    return {"body": ctx.fs.read("go.mod"), "probe": ctx.probe("gh.issue.list", ["x/y"])["out"]}
`)
	if out["body"] != "module kitsoki\n" || out["probe"] != "[]" {
		t.Errorf("round-trip mismatch: %v", out)
	}
}
