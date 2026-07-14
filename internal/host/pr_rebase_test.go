package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveRunTestsPythonSuiteConflictKeepsBothSuites(t *testing.T) {
	start := "<<<<<" + "<< HEAD\n"
	base := "||||" + "||| parent\n"
	mid := "====" + "===\n"
	end := ">>>>" + ">>> commit\n"
	input := "before\n" +
		start +
		"section \"python tool tests (session-mining + product-journey + arena)\"\n" +
		base +
		"section \"python tool tests (session-mining + product-journey)\"\n" +
		mid +
		"section \"python tool tests (session-mining + product-journey + dev-story scripts)\"\n" +
		end +
		"if command -v python3 >/dev/null 2>&1; then\n" +
		start +
		"\tMINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py tools/arena/tests/test_*.py)\n" +
		base +
		"\tMINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py)\n" +
		mid +
		"\tMINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py stories/dev-story/scripts/*_test.py)\n" +
		end +
		"fi\nafter\n"
	got, ok := resolveRunTestsPythonSuiteConflict(input)
	if !ok {
		t.Fatal("conflict was not resolved")
	}
	if strings.Contains(got, "<<<<<<<") || strings.Contains(got, ">>>>>>>") {
		t.Fatalf("conflict markers remain:\n%s", got)
	}
	for _, want := range []string{
		`section "python tool tests (session-mining + product-journey + arena + dev-story scripts)"`,
		"tools/arena/tests/test_*.py",
		"stories/dev-story/scripts/*_test.py",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("resolved content missing %q:\n%s", want, got)
		}
	}
}

func TestResolveRunTestsPythonSuiteConflictHandlesCombinedBlock(t *testing.T) {
	start := "<<<<<" + "<< HEAD\n"
	base := "||||" + "||| parent\n"
	mid := "====" + "===\n"
	end := ">>>>" + ">>> commit\n"
	input := "before\n" +
		start +
		"section \"python tool tests (session-mining + product-journey + arena)\"\n" +
		"if command -v python3 >/dev/null 2>&1; then\n" +
		"\tshopt -s nullglob\n" +
		"\tMINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py tools/arena/tests/test_*.py)\n" +
		base +
		"section \"python tool tests (session-mining + product-journey)\"\n" +
		"if command -v python3 >/dev/null 2>&1; then\n" +
		"\tshopt -s nullglob\n" +
		"\tMINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py)\n" +
		mid +
		"section \"python tool tests (session-mining + product-journey + dev-story scripts)\"\n" +
		"if command -v python3 >/dev/null 2>&1; then\n" +
		"\tshopt -s nullglob\n" +
		"\tMINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py stories/dev-story/scripts/*_test.py)\n" +
		end +
		"\tfor t in \"${MINING_TESTS[@]}\"; do\n" +
		"after\n"

	got, ok := resolveRunTestsPythonSuiteConflict(input)
	if !ok {
		t.Fatal("combined conflict was not resolved")
	}
	for _, want := range []string{
		`if command -v python3 >/dev/null 2>&1; then`,
		`	shopt -s nullglob`,
		"tools/arena/tests/test_*.py",
		"stories/dev-story/scripts/*_test.py",
		`for t in "${MINING_TESTS[@]}"; do`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("resolved content missing %q:\n%s", want, got)
		}
	}
}

func TestRepairRunTestsPythonSuiteStanzaRestoresMissingAssignment(t *testing.T) {
	input := `before
section "python tool tests (session-mining + product-journey + arena + dev-story scripts)"
	shopt -u nullglob
	for t in "${MINING_TESTS[@]}"; do
after
`
	got, changed := repairRunTestsPythonSuiteStanza(input)
	if !changed {
		t.Fatal("repair did not change broken stanza")
	}
	for _, want := range []string{
		`if command -v python3 >/dev/null 2>&1; then`,
		`	shopt -s nullglob`,
		`	MINING_TESTS=(tools/session-mining/tests/test_*.py tools/product-journey/*_test.py tools/arena/tests/test_*.py stories/dev-story/scripts/*_test.py)`,
		`	shopt -u nullglob`,
		`for t in "${MINING_TESTS[@]}"; do`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("repaired content missing %q:\n%s", want, got)
		}
	}
}

func TestGitVCS_PRRebaseCleanPathPushesHead(t *testing.T) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/o/r/pulls/56" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"head":{"ref":"fix/pr-56","sha":"oldsha","repo":{"full_name":"o/r"}},"base":{"ref":"main"}}`))
	}))
	defer srv.Close()
	restoreAPI := SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()
	restore := SetExecRunnerForTest(func(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
		key := name + " " + strings.Join(args, " ")
		calls = append(calls, key)
		if name == "gh" {
			t.Fatalf("pr_rebase must not invoke gh: %s", key)
		}
		if name != "git" {
			t.Fatalf("unexpected command: %s", key)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "AUTHORIZATION: basic eC1hY2Nlc3MtdG9rZW46YXBwLXRva2Vu") {
			t.Fatalf("git command missing token header: %s", joined)
		}
		if strings.Contains(joined, "rev-parse HEAD") {
			return "abc123\n", "", 0, nil
		}
		return "", "", 0, nil
	})
	defer restore()

	ctx := WithCLIExecEnv(context.Background(), map[string]string{"GH_TOKEN": "app-token"})
	res, err := GitVCSHandler(ctx, map[string]any{"op": "pr_rebase", "repo": "o/r", "pr_id": "56"})
	if err != nil {
		t.Fatalf("GitVCSHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain error: %s", res.Error)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{
		"fetch --no-tags origin main:refs/remotes/origin/main",
		"fetch --no-tags origin refs/pull/56/head:pr-56",
		"rebase refs/remotes/origin/main",
		"push --force-with-lease=fix/pr-56:oldsha head HEAD:fix/pr-56",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing command fragment %q in:\n%s", want, joined)
		}
	}
}
