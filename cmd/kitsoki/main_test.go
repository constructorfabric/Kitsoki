package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// execRoot runs the kitsoki cobra root with the given args, returning the
// combined stdout buffer and the error returned by Execute. Stderr is also
// captured into the same buffer because cobra writes help/usage there.
func execRoot(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.Execute()
	return buf.String(), err
}

func TestCLI_Version(t *testing.T) {
	// The version command writes via fmt.Printf, which bypasses cobra's
	// output streams. We can still verify it runs without error and that
	// help advertises it.
	if _, err := execRoot(t, "version"); err != nil {
		t.Fatalf("version: %v", err)
	}
	out, err := execRoot(t, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(out, "version") {
		t.Errorf("--help output does not mention version subcommand:\n%s", out)
	}
}

// TestCLI_TopLevelHelp checks that every registered subcommand responds to
// --help without an error. This catches missing required flags on the root
// command and broken doc strings.
func TestCLI_TopLevelHelp(t *testing.T) {
	subs := []string{
		"run", "viz", "trace", "replay", "test", "serve", "render",
		"docs", "record", "inspect", "turn", "session", "chat",
		"mcp", "mcp-test", "mcp-codeact", "mcp-validator", "agent-bench",
		"ticket-provider",
	}
	for _, sub := range subs {
		sub := sub
		t.Run(sub, func(t *testing.T) {
			out, err := execRoot(t, sub, "--help")
			if err != nil {
				t.Fatalf("%s --help: %v\n%s", sub, err, out)
			}
			if !strings.Contains(out, sub) {
				t.Errorf("%s --help output does not mention command name:\n%s", sub, out)
			}
		})
	}
}

func TestCLI_DocsListsTopics(t *testing.T) {
	out, err := execRoot(t, "docs")
	if err != nil {
		t.Fatalf("docs: %v", err)
	}
	// docs (no args) prints an index of embedded topics.
	for _, topic := range []string{"llm-guide", "app-schema"} {
		if !strings.Contains(out, topic) {
			t.Errorf("docs index missing topic %q:\n%s", topic, out)
		}
	}
}

func TestCLI_DocsAppSchemaPrints(t *testing.T) {
	out, err := execRoot(t, "docs", "app-schema")
	if err != nil {
		t.Fatalf("docs app-schema: %v", err)
	}
	if len(out) < 500 {
		t.Errorf("docs app-schema output suspiciously short (%d bytes)", len(out))
	}
}

// TestCLI_VizCloakProducesDOT runs the visualisation pipeline end-to-end on
// the cloak example. Default viz writes a file; we pass --out=<tempfile> so
// the test doesn't pollute the working directory.
func TestCLI_VizCloakProducesDOT(t *testing.T) {
	appYAML := filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")
	outPath := filepath.Join(t.TempDir(), "cloak.dot")
	if _, err := execRoot(t, "viz", appYAML, "--out", outPath); err != nil {
		t.Fatalf("viz: %v", err)
	}
	body, err := readFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	for _, want := range []string{"digraph", "foyer", "->"} {
		if !strings.Contains(body, want) {
			t.Errorf("viz output missing %q", want)
		}
	}
}

// TestCLI_VizCloakMermaid checks the --mermaid flag. Uses --out=- to write
// the diagram to stdout instead of a file in cwd.
func TestCLI_VizCloakMermaid(t *testing.T) {
	appYAML := filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")
	out, err := execRoot(t, "viz", "--mermaid", "--out", "-", appYAML)
	if err != nil {
		t.Fatalf("viz --mermaid: %v\n%s", err, out)
	}
	if !strings.Contains(out, "stateDiagram") && !strings.Contains(out, "flowchart") {
		t.Errorf("mermaid output missing diagram header:\n%s", out)
	}
}

// TestCLI_RenderCloakWritesMarkdown checks the render pipeline end-to-end.
func TestCLI_RenderCloakWritesMarkdown(t *testing.T) {
	appYAML := filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml")
	out, err := execRoot(t, "render", appYAML)
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}
	for _, want := range []string{"# Cloak of Darkness", "## State Diagram", "## Intents"} {
		if !strings.Contains(out, want) {
			t.Errorf("render output missing %q", want)
		}
	}
}

// TestCLI_UnknownSubcommandFails ensures the root rejects unknown commands.
func TestCLI_UnknownSubcommandFails(t *testing.T) {
	_, err := execRoot(t, "nope-not-a-real-subcommand")
	if err == nil {
		t.Fatal("expected unknown subcommand to error")
	}
}

func TestCLI_PersonaQAHelp(t *testing.T) {
	out, err := execRoot(t, "persona-qa", "--help")
	if err != nil {
		t.Fatalf("persona-qa --help: %v\n%s", err, out)
	}
	for _, want := range []string{"Persona QA compatibility adapter", "kitsoki run @kitsoki/scenario-qa", "preview project-onboarding across all transports", "check project-onboarding across all transports", "transports", "emit-run", "deck", "complete"} {
		if !strings.Contains(out, want) {
			t.Errorf("persona-qa help missing %q:\n%s", want, out)
		}
	}
}

func TestCLI_NoArgsDelegatesToRun(t *testing.T) {
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	t.Cleanup(func() {
		if chdirErr := os.Chdir(oldWd); chdirErr != nil {
			t.Fatalf("restore cwd: %v", chdirErr)
		}
	})
	if err := os.WriteFile(filepath.Join(tmp, ".kitsoki.yaml"), []byte("root:\n  import: definitely-not-a-story\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	out, err := execRoot(t)
	if err == nil {
		t.Fatal("expected bare kitsoki to fail on invalid run config")
	}
	if strings.Contains(out, "Usage:") {
		t.Fatalf("bare kitsoki printed help instead of entering run startup:\n%s", out)
	}
	if !strings.Contains(err.Error(), "definitely-not-a-story") {
		t.Fatalf("expected run config error, got %v", err)
	}
}

func TestCLI_NoArgsAcceptsRunFlags(t *testing.T) {
	out, err := execRoot(t, "--mode", "definitely-not-a-mode")
	if err == nil {
		t.Fatal("expected bare kitsoki to parse --mode and fail validation")
	}
	if strings.Contains(out, "Usage:") {
		t.Fatalf("bare kitsoki --mode printed help instead of entering run startup:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--mode") {
		t.Fatalf("expected mode validation error, got %v", err)
	}
}

func TestCLI_RunStartupErrorSuppressesLoaderWarnings(t *testing.T) {
	appPath := filepath.Join(t.TempDir(), "app.yaml")
	if err := os.WriteFile(appPath, []byte(`
app:
  id: warning-app
  version: 0.1.0
world:
  feature_branch_diff: { type: string, default: "(pending)" }
intents:
  go: {}
root: start
states:
  start:
    on_enter:
      - invoke: host.diff
        bind:
          feature_branch_diff: diff
    view: "{{ world.feature_branch_diff }}"
`), 0o644); err != nil {
		t.Fatalf("write app: %v", err)
	}

	var logBuf bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })

	out, err := execRoot(t, "run", appPath, "--mode", "definitely-not-a-mode")
	if err == nil {
		t.Fatal("expected run to fail before launching TUI")
	}
	if strings.Contains(logBuf.String(), "view references an on_enter bind-target") {
		t.Fatalf("loader advisory leaked to default slog during run startup:\n%s", logBuf.String())
	}
	if strings.Contains(out, "view references an on_enter bind-target") {
		t.Fatalf("loader advisory leaked to cobra output:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--mode") {
		t.Fatalf("expected mode validation error, got %v", err)
	}
}
