package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/harness"
)

func TestConversationalHarness_ToolAllowList(t *testing.T) {
	h := harness.NewConversationalHarness()
	tools := h.Tools()
	if len(tools) < 2 {
		t.Fatalf("expected at least 2 read-only tools, got %d", len(tools))
	}
	names := make(map[string]struct{})
	for _, tool := range tools {
		names[tool.Name] = struct{}{}
	}
	if _, ok := names["file_read"]; !ok {
		t.Error("expected file_read tool")
	}
	if _, ok := names["code_search"]; !ok {
		t.Error("expected code_search tool")
	}
}

func TestConversationalHarness_FileRead(t *testing.T) {
	h := harness.NewConversationalHarness()

	// Write a temp file.
	dir := t.TempDir()
	content := "hello oracle world"
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	out, err := h.InvokeToolByName(context.Background(), "file_read", map[string]any{
		"path": path,
	})
	if err != nil {
		t.Fatalf("file_read: %v", err)
	}
	if out != content {
		t.Fatalf("expected %q, got %q", content, out)
	}
}

func TestConversationalHarness_FileRead_Traversal(t *testing.T) {
	h := harness.NewConversationalHarness()
	_, err := h.InvokeToolByName(context.Background(), "file_read", map[string]any{
		"path": "/etc/../etc/passwd",
	})
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestConversationalHarness_CodeSearch(t *testing.T) {
	h := harness.NewConversationalHarness()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("func Hello() {}\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("func World() {}\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, err := h.InvokeToolByName(context.Background(), "code_search", map[string]any{
		"pattern": "Hello",
		"dir":     dir,
	})
	if err != nil {
		t.Fatalf("code_search: %v", err)
	}
	if !strings.Contains(out, "Hello") {
		t.Fatalf("expected Hello in output, got %q", out)
	}
	if strings.Contains(out, "World") {
		t.Fatalf("expected only Hello matches, not World")
	}
}

func TestConversationalHarness_UnknownTool(t *testing.T) {
	h := harness.NewConversationalHarness()
	_, err := h.InvokeToolByName(context.Background(), "exec_shell", map[string]any{
		"cmd": "rm -rf /",
	})
	if err == nil {
		t.Fatal("expected error for tool not in allow-list")
	}
}

func TestConversationalHarness_RunConversational(t *testing.T) {
	h := harness.NewConversationalHarness()
	result, err := h.RunConversational(context.Background(), harness.ConversationalInput{
		UserText: "What files are in /tmp?",
	})
	if err != nil {
		t.Fatalf("RunConversational: %v", err)
	}
	if result.Markdown == "" {
		t.Fatal("expected non-empty Markdown response")
	}
}
