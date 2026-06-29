package main

import (
	"bytes"
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
		"mcp", "mcp-test", "mcp-validator", "agent-bench",
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
