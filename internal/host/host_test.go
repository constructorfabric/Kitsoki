package host_test

import (
	"context"
	"testing"

	"hally/internal/host"
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
