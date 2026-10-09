package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/host"
)

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := host.NewRegistry()
	called := false
	r.Register("host.test", func(ctx context.Context, args map[string]any) (host.Result, error) {
		called = true
		return host.Result{Data: map[string]any{"echo": args["msg"]}}, nil
	})

	h, ok := r.Get("host.test")
	if !ok {
		t.Fatal("expected handler to be registered")
	}

	result, err := h(context.Background(), map[string]any{"msg": "hello"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
	if result.Data["echo"] != "hello" {
		t.Fatalf("expected echo=hello, got %v", result.Data["echo"])
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := host.NewRegistry()
	r.Register("host.dup", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{}, nil
	})
	defer func() {
		if rec := recover(); rec == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r.Register("host.dup", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{}, nil
	})
}

// TestRegistry_Invoke_PrefixFallbackInjectsOp guards the registry's
// prefix-fallback contract: when a name like `host.local_files.ticket.search`
// has no exact handler but a shorter prefix (`host.local_files.ticket`)
// is registered, the dropped trailing segment is injected into args as
// "op" so the shared handler can dispatch on it.
//
// Regression for the dev-story dogfood: `iface.ticket.search` resolved
// to `host.local_files.ticket.search` at the call site, but the only
// registered handler was `host.local_files.ticket` (which switches on
// args["op"]). Without the suffix injection the handler always saw
// op="" and returned "op argument is required" — which then triggered
// the room's `on_error` arc and prevented the ticket list from rendering.
func TestRegistry_Invoke_PrefixFallbackInjectsOp(t *testing.T) {
	r := host.NewRegistry()
	var capturedOp string
	r.Register("host.thing", func(ctx context.Context, args map[string]any) (host.Result, error) {
		capturedOp, _ = args["op"].(string)
		return host.Result{Data: map[string]any{"ok": true}}, nil
	})

	// Exact match: op is not injected.
	capturedOp = "should-stay-empty"
	_, err := r.Invoke(context.Background(), "host.thing", map[string]any{})
	if err != nil {
		t.Fatalf("exact-match invoke error: %v", err)
	}
	if capturedOp != "" {
		t.Fatalf("exact-match should not inject op; got %q", capturedOp)
	}

	// Suffix fallback: trailing segment lands in args["op"].
	_, err = r.Invoke(context.Background(), "host.thing.search", map[string]any{})
	if err != nil {
		t.Fatalf("suffix-fallback invoke error: %v", err)
	}
	if capturedOp != "search" {
		t.Fatalf("suffix-fallback should inject op=\"search\"; got %q", capturedOp)
	}

	// Multi-segment suffix: full tail joins.
	_, err = r.Invoke(context.Background(), "host.thing.deep.nested", map[string]any{})
	if err != nil {
		t.Fatalf("nested-suffix invoke error: %v", err)
	}
	if capturedOp != "deep.nested" {
		t.Fatalf("nested-suffix should inject op=\"deep.nested\"; got %q", capturedOp)
	}

	// Caller-supplied op wins — the registry must not clobber.
	_, err = r.Invoke(context.Background(), "host.thing.search", map[string]any{"op": "caller-wins"})
	if err != nil {
		t.Fatalf("caller-op invoke error: %v", err)
	}
	if capturedOp != "caller-wins" {
		t.Fatalf("caller-supplied op should win; got %q", capturedOp)
	}

	// Nil args: should still inject without panicking.
	capturedOp = ""
	_, err = r.Invoke(context.Background(), "host.thing.search", nil)
	if err != nil {
		t.Fatalf("nil-args invoke error: %v", err)
	}
	if capturedOp != "search" {
		t.Fatalf("nil-args path should inject op=\"search\"; got %q", capturedOp)
	}
}

func TestRegistry_NotFound(t *testing.T) {
	r := host.NewRegistry()
	_, err := r.Invoke(context.Background(), "host.missing", nil)
	if err == nil {
		t.Fatal("expected error for missing handler")
	}
}

func TestRegistry_ValidateAllowList(t *testing.T) {
	r := host.NewRegistry()
	r.Register("host.a", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{}, nil
	})

	// Passes when all declared hosts are registered.
	if err := r.ValidateAllowList([]string{"host.a"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Fails when a declared host is not registered.
	if err := r.ValidateAllowList([]string{"host.a", "host.missing"}); err == nil {
		t.Fatal("expected error for missing host")
	}
}

func TestSecretsContext(t *testing.T) {
	ctx := context.Background()
	secrets := map[string]string{"API_KEY": "abc123"}
	ctx = host.WithSecrets(ctx, secrets)

	got := host.SecretsFromContext(ctx)
	if got["API_KEY"] != "abc123" {
		t.Fatalf("expected API_KEY=abc123, got %v", got)
	}
}

func TestRegistry_InvokeInjectsLoadedSecrets(t *testing.T) {
	t.Setenv("KITSOKI_TEST_SECRET", "abc123")
	r := host.NewRegistry()
	r.Register("host.secret_probe", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{
			"secret": host.SecretsFromContext(ctx)["KITSOKI_TEST_SECRET"],
		}}, nil
	})

	res, err := r.Invoke(context.Background(), "host.secret_probe", nil)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if res.Data["secret"] != "abc123" {
		t.Fatalf("secret = %v, want abc123", res.Data["secret"])
	}
}

func TestRunHandler(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd": "echo hello",
	})
	if err != nil {
		t.Fatalf("host.run error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("host.run domain error: %v", result.Error)
	}
	stdout, _ := result.Data["stdout"].(string)
	if stdout == "" {
		t.Fatal("expected non-empty stdout")
	}
	ok, _ := result.Data["ok"].(bool)
	if !ok {
		t.Fatal("expected ok=true")
	}
}

func TestRunHandler_MissingCmd(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected infra error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected domain error for missing cmd")
	}
}

func TestRunHandler_NonZeroExit(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd": "exit 1",
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	ok, _ := result.Data["ok"].(bool)
	if ok {
		t.Fatal("expected ok=false for non-zero exit")
	}
	exitCode, _ := result.Data["exit_code"].(int)
	if exitCode != 1 {
		t.Fatalf("expected exit_code=1, got %v", exitCode)
	}
	// Default behaviour: Result.Error stays empty so the success `done`
	// arc fires.  Callers that want failure routing must opt in with
	// fail_on_error=true (see TestRunHandler_FailOnError).
	if result.Error != "" {
		t.Fatalf("expected empty Result.Error by default, got %q", result.Error)
	}
}

func TestRunHandler_FailOnError(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd":           "exit 7",
		"fail_on_error": true,
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected non-empty Result.Error when fail_on_error=true and exit != 0")
	}
	// Data should still be populated so the error state can render
	// stdout / exit_code into a useful diagnostic.
	exitCode, _ := result.Data["exit_code"].(int)
	if exitCode != 7 {
		t.Fatalf("expected exit_code=7 alongside the error, got %v", exitCode)
	}
}

// TestRunHandler_Timeout pins the fix for the silent session wedge: a child
// that never returns (here a `sleep` far longer than the cap, standing in for
// an HTTP client blocked on a half-closed proxy socket) must be killed and
// surfaced as an on_error-routable Result.Error, NOT block the handler — which
// would hold the session driver lock and freeze every subsequent turn.
func TestRunHandler_Timeout(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	done := make(chan struct{})
	var result host.Result
	var err error
	go func() {
		result, err = r.Invoke(context.Background(), "host.run", map[string]any{
			"cmd":     "sleep 30",
			"timeout": 1, // seconds
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("host.run with timeout=1 did not return — the timeout did not kill the child (the wedge bug)")
	}

	if err != nil {
		t.Fatalf("timeout should be a domain error, not infra error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected non-empty Result.Error on timeout so the on_error arc fires")
	}
	if !strings.Contains(result.Error, "timed out") {
		t.Fatalf("expected a 'timed out' error, got %q", result.Error)
	}
	if to, _ := result.Data["timed_out"].(bool); !to {
		t.Fatal("expected Data.timed_out=true")
	}
}

// TestRunHandler_TimeoutNotHit confirms the cap is transparent when the
// command finishes inside it: normal success, no timeout flag.
func TestRunHandler_TimeoutNotHit(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd":     "echo quick",
		"timeout": "5s",
	})
	if err != nil {
		t.Fatalf("unexpected infra error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("expected no error well inside the cap, got %q", result.Error)
	}
	if to, _ := result.Data["timed_out"].(bool); to {
		t.Fatal("did not expect timed_out=true for a fast command")
	}
}

func TestRunHandler_BadTimeout(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd":     "echo hi",
		"timeout": "not-a-duration",
	})
	if err != nil {
		t.Fatalf("unexpected infra error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected a loud domain error for an unparseable timeout")
	}
}

func TestRunHandler_FailOnError_ZeroExit(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd":           "true",
		"fail_on_error": true,
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	// Zero exit + fail_on_error=true is the success path: no error.
	if result.Error != "" {
		t.Fatalf("expected empty Result.Error on zero exit, got %q", result.Error)
	}
}

// TestRunHandler_StdoutJSONBinding covers the host.run convenience that
// parses stdout's last non-empty line as JSON and exposes it under
// `stdout_json` for binding.  This is the contract the bugfix room
// relies on: subcommands emit logs to stderr (which CombinedOutput
// drops onto stdout) plus a single JSON envelope line; the bound slot
// gets the structured envelope, not the prose.
func TestRunHandler_StdoutJSONBinding(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	// Multi-line output: prose lines, then a JSON line at the end.
	cmd := `echo '2026-04-29 INFO  starting' >&2
echo 'work in progress'
echo '{"ok":true,"data":{"k":"v"}}'`

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd": cmd,
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected domain error: %v", result.Error)
	}
	parsed, ok := result.Data["stdout_json"].(map[string]any)
	if !ok {
		t.Fatalf("stdout_json missing or wrong shape: %T %v",
			result.Data["stdout_json"], result.Data["stdout_json"])
	}
	if parsed["ok"] != true {
		t.Fatalf("stdout_json.ok: want true, got %v", parsed["ok"])
	}
	dataField, ok := parsed["data"].(map[string]any)
	if !ok {
		t.Fatalf("stdout_json.data missing: %v", parsed)
	}
	if dataField["k"] != "v" {
		t.Fatalf("stdout_json.data.k: want %q, got %v", "v", dataField["k"])
	}
}

// TestRunHandler_StdoutJSONBinding_NotJSON confirms that plain-text
// stdout doesn't populate stdout_json (and doesn't surface a
// parse-error either, because nothing about the output looks JSON-ish).
func TestRunHandler_StdoutJSONBinding_NotJSON(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd": "echo hello world",
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	if _, present := result.Data["stdout_json"]; present {
		t.Fatalf("stdout_json should be absent for plain text, got %v", result.Data["stdout_json"])
	}
	if _, present := result.Data["stdout_json_parse_error"]; present {
		t.Fatal("stdout_json_parse_error should be absent for plain text — only set when last line looks JSON-ish")
	}
}

// TestRunHandler_StdoutJSONBinding_MalformedJSON verifies the
// diagnostic surface: the last line *looks* like JSON (starts with `{`)
// but doesn't parse.  We expose the parse error under
// `stdout_json_parse_error` so debugging the envelope drift is easy.
func TestRunHandler_StdoutJSONBinding_MalformedJSON(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd": `echo '{not valid json'`,
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	if _, present := result.Data["stdout_json"]; present {
		t.Fatal("stdout_json should be absent on parse failure")
	}
	if msg, _ := result.Data["stdout_json_parse_error"].(string); msg == "" {
		t.Fatal("stdout_json_parse_error should be populated when last line looks JSON-ish but won't parse")
	}
}

// TestRunHandler_StdoutJSONBinding_PrettyPrinted is the regression guard for
// the silent-binding-loss footgun that stranded git-ops's real (non-mocked)
// host.run routing: a script that emits a PRETTY-PRINTED JSON envelope (the
// default `jq -n '{...}'` output spans multiple lines and ends with a bare
// "}") used to bind nothing, because only stdout's last non-empty line was
// parsed. The whole-blob fallback now parses the multi-line object. This is
// exactly the shape git-ops/rooms/idle.yaml's detect_context emits.
func TestRunHandler_StdoutJSONBinding_PrettyPrinted(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	// jq's default (no -c) pretty-prints; mimic that exactly.
	cmd := `echo '2026-06-20 INFO  detecting' >&2
echo '{'
echo '  "route": "on_branch",'
echo '  "branch": "feature",'
echo '  "commits_ahead": 1'
echo '}'`

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{"cmd": cmd})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected domain error: %v", result.Error)
	}
	parsed, ok := result.Data["stdout_json"].(map[string]any)
	if !ok {
		t.Fatalf("pretty-printed JSON must bind to stdout_json; got %T %v",
			result.Data["stdout_json"], result.Data["stdout_json"])
	}
	if parsed["route"] != "on_branch" {
		t.Fatalf("stdout_json.route: want on_branch, got %v", parsed["route"])
	}
	if _, present := result.Data["stdout_json_parse_error"]; present {
		t.Fatalf("no parse error expected on a valid multi-line envelope; got %v",
			result.Data["stdout_json_parse_error"])
	}
}

// TestRunHandler_StdoutJSONBinding_LogsThenPretty confirms the trailing
// extraction finds the envelope amid preceding prose: leading log lines on
// stdout (whether from stderr via CombinedOutput, or a script that logs to
// stdout) followed by a pretty-printed JSON block bind to that trailing
// block. This is the whole point of the contract — pluck the JSON envelope
// out of mixed log+JSON output.
func TestRunHandler_StdoutJSONBinding_LogsThenPretty(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	cmd := `echo 'work in progress on stdout'
echo '{'
echo '  "route": "on_branch"'
echo '}'`

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{"cmd": cmd})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	parsed, ok := result.Data["stdout_json"].(map[string]any)
	if !ok {
		t.Fatalf("trailing JSON envelope must be extracted from amid prose; got %T %v",
			result.Data["stdout_json"], result.Data["stdout_json"])
	}
	if parsed["route"] != "on_branch" {
		t.Fatalf("stdout_json.route: want on_branch, got %v", parsed["route"])
	}
}

// TestRunHandler_ArgsArgvMode covers the host.run argv form: when an
// `args:` list is supplied, the command runs with those positional
// arguments directly via exec — no shell, no word-splitting, no tilde
// expansion.  This is the bug-fix for the jira_search room, where a JQL
// query containing spaces and `component ~ "..."` was being shell-split
// and tilde-expanded into garbage by the bash-mode codepath.
func TestRunHandler_ArgsArgvMode(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	jql := `issuetype = Bug AND resolution = Unresolved AND component ~ "presentation service"`

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd": "printf",
		// printf "[%s]\n" arg1 arg2 ... — emits one line per argv entry.
		"args": []any{"[%s]\n", jql, "--limit", "25"},
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected domain error: %v", result.Error)
	}
	stdout, _ := result.Data["stdout"].(string)
	want := "[" + jql + "]\n[--limit]\n[25]\n"
	if stdout != want {
		t.Fatalf("argv mode: stdout mismatch\n got: %q\nwant: %q", stdout, want)
	}
}

// TestRunHandler_ScriptPathIsAppRelative pins the imported-story-safe script
// contract. The script is a separate argv element resolved from the app root,
// rather than shell text that expands the process-global KITSOKI_APP_DIR (which
// can name a parent wrapper or be unset in a long-lived driver).
func TestRunHandler_ScriptPathIsAppRelative(t *testing.T) {
	appDir := t.TempDir()
	scriptPath := filepath.Join(appDir, "scripts", "echo.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scriptPath, []byte("printf '%s\\n' \"$1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(host.AppDirEnv, appDir)

	result, err := host.RunHandler(context.Background(), map[string]any{
		"cmd":    "bash",
		"script": "scripts/echo.sh",
		"args":   []any{"onboarding-ok"},
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected domain error: %v", result.Error)
	}
	if got := result.Data["stdout"]; got != "onboarding-ok\n" {
		t.Fatalf("stdout = %q, want %q", got, "onboarding-ok\\n")
	}
}

// TestRunHandler_ArgsMapJSONSerialised covers the world-slot-on-argv path:
// when an args list element is a Go map (i.e. an `obj`-typed world slot
// like `phase_6_5_submitted`), it's serialised to compact JSON before
// reaching argv.  Without this, `args: ["{{ world.payload }}"]` would
// fail with "unsupported type map[string]interface {}".  The bugfix
// room's verify-impl step relies on this contract.
func TestRunHandler_ArgsMapJSONSerialised(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	payload := map[string]any{
		"summary":       "fixed",
		"commit_hashes": []any{"deadbeef"},
		"files_changed": []any{
			map[string]any{"path": "x.go", "action": "modified"},
		},
	}

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd":  "printf",
		"args": []any{"%s", payload},
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected domain error: %v", result.Error)
	}
	stdout, _ := result.Data["stdout"].(string)
	// Output is the JSON-encoded payload — confirm a couple of keys
	// survived to the argv element.
	if stdout == "" {
		t.Fatalf("expected JSON payload on stdout, got empty")
	}
	for _, want := range []string{`"summary":"fixed"`, `"commit_hashes":["deadbeef"]`, `"path":"x.go"`} {
		if !contains(stdout, want) {
			t.Fatalf("stdout missing %q\nstdout: %s", want, stdout)
		}
	}
}

func TestRunHandler_ArgsSliceJSONSerialised(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	payload := []any{"a", "b", "c"}
	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd":  "printf",
		"args": []any{"%s", payload},
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected domain error: %v", result.Error)
	}
	stdout, _ := result.Data["stdout"].(string)
	if stdout != `["a","b","c"]` {
		t.Fatalf("slice arg: stdout mismatch\n got: %q\nwant: %q", stdout, `["a","b","c"]`)
	}
}

// contains is a tiny strings.Contains shim so the test file doesn't
// import strings just for one call.
func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || hasSubstr(s, sub)))
}

func hasSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestRunHandler_ArgsRejectsNonList confirms that `args:` must be a list.
// A scalar (or other shape) is a domain error rather than a silent fallback
// to bash-mode, so authors can't accidentally pass `args: foo` and have it
// disappear into the void.
func TestRunHandler_ArgsRejectsNonList(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd":  "echo",
		"args": "not-a-list",
	})
	if err != nil {
		t.Fatalf("unexpected infra error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected domain error when args is not a list")
	}
}

// TestRunHandler_BashModeStillWorks pins the legacy `cmd:`-only contract
// so the argv-mode addition can't accidentally regress callers that rely
// on shell features (pipes, redirects, glob expansion).
func TestRunHandler_BashModeStillWorks(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	result, err := r.Invoke(context.Background(), "host.run", map[string]any{
		"cmd": "echo one && echo two",
	})
	if err != nil {
		t.Fatalf("host.run infra error: %v", err)
	}
	stdout, _ := result.Data["stdout"].(string)
	if stdout != "one\ntwo\n" {
		t.Fatalf("bash-mode regression: got %q, want %q", stdout, "one\ntwo\n")
	}
}
