package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runCassetteCmd executes the cassette subcommand tree with the given args
// against the root cobra command. Returns (stdout, stderr, error).
func runCassetteCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := newRootCmd()

	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)

	root.SetArgs(append([]string{"cassette"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

// writeTempCassette writes a minimal cassette YAML to a temp file and returns its path.
func writeTempCassette(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write cassette %q: %v", p, err)
	}
	return p
}

// minimalCassette returns a valid single-episode cassette YAML string.
func minimalCassette(id, handler string) string {
	return "kind: host_cassette\napp_id: test\nepisodes:\n" +
		"  - id: " + id + "\n" +
		"    match:\n" +
		"      handler: " + handler + "\n" +
		"    response:\n" +
		"      data: {result: ok}\n"
}

// ── diff tests ──────────────────────────────────────────────────────────────

func TestCassetteDiff_Identical(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cas := minimalCassette("ep1", "host.foo")
	old := writeTempCassette(t, dir, "old.yaml", cas)
	new := writeTempCassette(t, dir, "new.yaml", cas)

	out, _, err := runCassetteCmd(t, "diff", old, new)
	if err != nil {
		t.Fatalf("expected no error for identical cassettes, got: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty output for identical cassettes, got: %q", out)
	}
}

func TestCassetteDiff_Added(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	oldCas := minimalCassette("ep1", "host.foo")
	newCas := minimalCassette("ep1", "host.foo") +
		"  - id: ep2\n" +
		"    match:\n" +
		"      handler: host.bar\n" +
		"    response:\n" +
		"      data: {ok: true}\n"

	old := writeTempCassette(t, dir, "old.yaml", oldCas)
	new := writeTempCassette(t, dir, "new.yaml", newCas)

	out, _, err := runCassetteCmd(t, "diff", old, new)
	if err == nil {
		t.Fatal("expected non-zero exit for differing cassettes, got nil error")
	}
	if !strings.Contains(out, "+ ep2") {
		t.Errorf("expected '+ ep2' in output, got:\n%s", out)
	}
	if strings.Contains(out, "- ep1") || strings.Contains(out, "~ ep1") {
		t.Errorf("unexpected diff for unchanged ep1 in output:\n%s", out)
	}
}

func TestCassetteDiff_Removed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	oldCas := minimalCassette("ep1", "host.foo") +
		"  - id: ep_old\n" +
		"    match:\n" +
		"      handler: host.baz\n" +
		"    response:\n" +
		"      data: {ok: true}\n"
	newCas := minimalCassette("ep1", "host.foo")

	old := writeTempCassette(t, dir, "old.yaml", oldCas)
	new := writeTempCassette(t, dir, "new.yaml", newCas)

	out, _, err := runCassetteCmd(t, "diff", old, new)
	if err == nil {
		t.Fatal("expected non-zero exit for differing cassettes")
	}
	if !strings.Contains(out, "- ep_old") {
		t.Errorf("expected '- ep_old' in output, got:\n%s", out)
	}
}

func TestCassetteDiff_Changed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	oldCas := minimalCassette("ep1", "host.foo")
	// Change the response data.
	newCas := "kind: host_cassette\napp_id: test\nepisodes:\n" +
		"  - id: ep1\n" +
		"    match:\n" +
		"      handler: host.foo\n" +
		"    response:\n" +
		"      data: {result: changed}\n"

	old := writeTempCassette(t, dir, "old.yaml", oldCas)
	new := writeTempCassette(t, dir, "new.yaml", newCas)

	out, _, err := runCassetteCmd(t, "diff", old, new)
	if err == nil {
		t.Fatal("expected non-zero exit for changed cassettes")
	}
	if !strings.Contains(out, "~ ep1") {
		t.Errorf("expected '~ ep1' in output, got:\n%s", out)
	}
}

func TestCassetteDiff_VerbosePrintsMatched(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cas := minimalCassette("ep1", "host.foo")
	old := writeTempCassette(t, dir, "old.yaml", cas)
	new := writeTempCassette(t, dir, "new.yaml", cas)

	out, _, err := runCassetteCmd(t, "diff", "--verbose", old, new)
	if err != nil {
		t.Fatalf("expected no error for identical cassettes: %v", err)
	}
	if !strings.Contains(out, "  ep1") {
		t.Errorf("expected '  ep1' (matched) with --verbose, got:\n%s", out)
	}
}

func TestCassetteDiff_JSON(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	oldCas := minimalCassette("ep1", "host.foo")
	newCas := minimalCassette("ep1", "host.foo") +
		"  - id: ep2\n" +
		"    match:\n" +
		"      handler: host.bar\n" +
		"    response:\n" +
		"      data: {ok: true}\n"

	old := writeTempCassette(t, dir, "old.yaml", oldCas)
	new := writeTempCassette(t, dir, "new.yaml", newCas)

	out, _, _ := runCassetteCmd(t, "diff", "--json", old, new)

	var result cassetteDiffResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("JSON output is invalid: %v\noutput:\n%s", err, out)
	}
	if len(result.Added) != 1 || result.Added[0] != "ep2" {
		t.Errorf("expected added=[ep2], got: %+v", result.Added)
	}
	if len(result.Removed) != 0 {
		t.Errorf("expected removed=[], got: %+v", result.Removed)
	}
	if len(result.Changed) != 0 {
		t.Errorf("expected changed=[], got: %+v", result.Changed)
	}
}

func TestCassetteDiff_JSON_ChangedFieldDiff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	oldCas := minimalCassette("ep1", "host.foo")
	newCas := "kind: host_cassette\napp_id: test\nepisodes:\n" +
		"  - id: ep1\n" +
		"    match:\n" +
		"      handler: host.foo\n" +
		"    response:\n" +
		"      data: {result: changed}\n"

	old := writeTempCassette(t, dir, "old.yaml", oldCas)
	new := writeTempCassette(t, dir, "new.yaml", newCas)

	out, _, _ := runCassetteCmd(t, "diff", "--json", old, new)

	var result cassetteDiffResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("JSON output is invalid: %v\noutput:\n%s", err, out)
	}
	if len(result.Changed) != 1 || result.Changed[0].ID != "ep1" {
		t.Errorf("expected changed=[{id:ep1,...}], got: %+v", result.Changed)
	}
	if result.Changed[0].ResponseDiff == nil {
		t.Error("expected response_diff to be set for changed episode")
	}
}

// ── lint tests ──────────────────────────────────────────────────────────────

func TestCassetteLint_Valid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cas := minimalCassette("ep1", "host.foo")
	path := writeTempCassette(t, dir, "cass.yaml", cas)

	out, _, err := runCassetteCmd(t, "lint", path)
	if err != nil {
		t.Fatalf("expected clean lint to succeed, got: %v\nout: %s", err, out)
	}
	if out != "" {
		t.Errorf("expected empty output for clean cassette, got: %q", out)
	}
}

func TestCassetteLint_DuplicateID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cas := "kind: host_cassette\napp_id: test\nepisodes:\n" +
		"  - id: ep1\n" +
		"    match:\n" +
		"      handler: host.foo\n" +
		"    response:\n" +
		"      data: {ok: true}\n" +
		"  - id: ep1\n" +
		"    match:\n" +
		"      handler: host.bar\n" +
		"    response:\n" +
		"      data: {ok: true}\n"
	path := writeTempCassette(t, dir, "cass.yaml", cas)

	out, _, err := runCassetteCmd(t, "lint", path)
	if err == nil {
		t.Fatal("expected lint to fail for duplicate id")
	}
	if !strings.Contains(out, "ERROR:") || !strings.Contains(out, "duplicate") {
		t.Errorf("expected duplicate id error in output, got:\n%s", out)
	}
	if !strings.Contains(out, "ep1") {
		t.Errorf("expected episode id in error message, got:\n%s", out)
	}
}

func TestCassetteLint_MissingInclude(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	cas := "kind: host_cassette\napp_id: test\nepisodes:\n" +
		"  - id: ep1\n" +
		"    match:\n" +
		"      handler: host.foo\n" +
		"    response:\n" +
		"      data: !include nonexistent.json\n"
	path := writeTempCassette(t, dir, "cass.yaml", cas)

	out, _, err := runCassetteCmd(t, "lint", path)
	if err == nil {
		t.Fatal("expected lint to fail for missing include")
	}
	if !strings.Contains(out, "ERROR:") {
		t.Errorf("expected ERROR in output, got:\n%s", out)
	}
}

func TestCassetteLint_OrphanedEpisode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write a minimal app that dispatches host.agent.ask but NOT host.ghost.
	appYAML := `app:
  id: test-orphan
  version: 0.1.0

hosts:
  - host.agent.ask

root: idle

intents:
  ask:
    title: Ask
    description: Ask something
    examples: ["ask"]

states:
  idle:
    description: "Idle"
    view: "Idle."
    on:
      ask:
        - target: idle
          effects:
            - invoke: host.agent.ask
              with: {q: hello}
              bind: {result: result}
`
	appPath := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(appPath, []byte(appYAML), 0644); err != nil {
		t.Fatalf("write app.yaml: %v", err)
	}

	// Cassette references host.agent.ask (present) AND host.ghost (orphan).
	cas := "kind: host_cassette\napp_id: test-orphan\nepisodes:\n" +
		"  - id: ep_real\n" +
		"    match:\n" +
		"      handler: host.agent.ask\n" +
		"    response:\n" +
		"      data: {result: ok}\n" +
		"  - id: ep_orphan\n" +
		"    match:\n" +
		"      handler: host.ghost\n" +
		"    response:\n" +
		"      data: {result: ok}\n"
	casPath := writeTempCassette(t, dir, "cass.yaml", cas)

	out, _, err := runCassetteCmd(t, "lint", casPath, "--against-app", appPath)
	if err == nil {
		t.Fatal("expected lint to fail for orphaned episode")
	}
	if !strings.Contains(out, "ep_orphan") {
		t.Errorf("expected orphaned episode id in output, got:\n%s", out)
	}
	if !strings.Contains(out, "host.ghost") {
		t.Errorf("expected orphaned handler name in output, got:\n%s", out)
	}
	// The real episode should not be flagged.
	if strings.Contains(out, "ep_real") {
		t.Errorf("ep_real should not be flagged, got:\n%s", out)
	}
}

func TestCassetteLint_NoOrphanWhenHandlerPresent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	appYAML := `app:
  id: test-noorphan
  version: 0.1.0

hosts:
  - host.agent.ask

root: idle

intents:
  ask:
    title: Ask
    description: Ask something
    examples: ["ask"]

states:
  idle:
    description: "Idle"
    view: "Idle."
    on:
      ask:
        - target: idle
          effects:
            - invoke: host.agent.ask
              with: {q: hello}
              bind: {result: result}
`
	appPath := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(appPath, []byte(appYAML), 0644); err != nil {
		t.Fatalf("write app.yaml: %v", err)
	}

	cas := minimalCassette("ep1", "host.agent.ask")
	casPath := writeTempCassette(t, dir, "cass.yaml", cas)

	out, _, err := runCassetteCmd(t, "lint", casPath, "--against-app", appPath)
	if err != nil {
		t.Fatalf("expected clean lint for non-orphaned episode, got: %v\nout: %s", err, out)
	}
}

func TestCassetteLint_OrphanOnEnter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Handler invoked in on_enter, not in transition effects.
	appYAML := `app:
  id: test-onenter
  version: 0.1.0

hosts:
  - host.data.load

root: loading

intents:
  go:
    title: Go
    description: Proceed
    examples: ["go"]

states:
  loading:
    description: "Loading"
    view: "Loading..."
    on_enter:
      - invoke: host.data.load
        with: {key: init}
        bind: {data: result}
    on:
      go:
        - target: loading
`
	appPath := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(appPath, []byte(appYAML), 0644); err != nil {
		t.Fatalf("write app.yaml: %v", err)
	}

	cas := minimalCassette("ep1", "host.data.load")
	casPath := writeTempCassette(t, dir, "cass.yaml", cas)

	out, _, err := runCassetteCmd(t, "lint", casPath, "--against-app", appPath)
	if err != nil {
		t.Fatalf("expected clean lint for on_enter handler, got: %v\nout: %s", err, out)
	}
}

func TestCassetteLint_StrictMode(t *testing.T) {
	t.Parallel()
	// Without --strict, a clean cassette with no warnings should pass.
	dir := t.TempDir()
	cas := minimalCassette("ep1", "host.foo")
	path := writeTempCassette(t, dir, "cass.yaml", cas)

	_, _, err := runCassetteCmd(t, "lint", "--strict", path)
	if err != nil {
		t.Fatalf("--strict with no warnings should still pass: %v", err)
	}
}
