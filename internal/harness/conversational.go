// Package harness — ConversationalHarness for Oracle Room free-form Q&A (§7).
//
// The ConversationalHarness supports tool-use with a read-only tool allow-list:
//   - file_read: read a file by path
//   - code_search: grep for a pattern in a directory
//
// It does NOT advance the state machine — responses are returned as Markdown text
// for direct display. The user exits via the "back" intent which pops the room
// history stack (§5).
//
// This harness is entered when a state declares mode: conversational (Oracle Room).
// It is stateless within a session: each call is independent (though callers may
// pass conversation history in the system prompt).
package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConversationalTool is a read-only tool available in the Oracle harness.
type ConversationalTool struct {
	// Name is the tool identifier.
	Name string
	// Description is shown in the LLM's system prompt.
	Description string
	// Call executes the tool with the given args and returns Markdown output.
	Call func(ctx context.Context, args map[string]any) (string, error)
}

// ConversationalResult is the response from the Oracle harness.
type ConversationalResult struct {
	// Markdown is the LLM's response for direct display.
	Markdown string
	// ToolCalls records any tool invocations made during this turn.
	ToolCalls []ConversationalToolCall
}

// ConversationalToolCall records one tool invocation during an Oracle turn.
type ConversationalToolCall struct {
	Name   string
	Args   map[string]any
	Output string
	Error  string
}

// ConversationalInput is the input to RunConversational.
type ConversationalInput struct {
	// UserText is the user's question.
	UserText string
	// SystemPrompt provides app/session context.
	SystemPrompt string
	// WorkingDir, if set, scopes file/search operations.
	WorkingDir string
}

// ConversationalHarness is a read-only Q&A harness for the Oracle Room (§7).
// It uses a stub implementation that exercises the tool allow-list without
// requiring a live LLM connection. The live implementation would use the
// Anthropic SDK with tool_use.
type ConversationalHarness struct {
	tools []ConversationalTool
}

// NewConversationalHarness creates a ConversationalHarness with the built-in
// read-only tool allow-list.
func NewConversationalHarness() *ConversationalHarness {
	h := &ConversationalHarness{}
	h.tools = builtinConversationalTools()
	return h
}

// Tools returns the harness's read-only tool allow-list.
func (h *ConversationalHarness) Tools() []ConversationalTool {
	return h.tools
}

// RunConversational handles one Oracle Room turn.
// In this stub implementation, it echoes the user's question wrapped as
// Markdown with available tool info. A live implementation would call the
// Anthropic API with tool_use enabled.
func (h *ConversationalHarness) RunConversational(ctx context.Context, in ConversationalInput) (ConversationalResult, error) {
	// Stub: reflect the query back with tool list.
	// A production implementation would call the LLM here.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Oracle Response** (stub mode)\n\n"))
	sb.WriteString(fmt.Sprintf("Your question: %s\n\n", in.UserText))
	sb.WriteString("Available read-only tools:\n")
	for _, t := range h.tools {
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n", t.Name, t.Description))
	}
	return ConversationalResult{Markdown: sb.String()}, nil
}

// InvokeToolByName calls a named tool with the given args.
// Returns an error if the tool is not in the allow-list.
func (h *ConversationalHarness) InvokeToolByName(ctx context.Context, name string, args map[string]any) (string, error) {
	for _, t := range h.tools {
		if t.Name == name {
			return t.Call(ctx, args)
		}
	}
	return "", fmt.Errorf("oracle: tool %q is not in the read-only allow-list", name)
}

// builtinConversationalTools returns the read-only tool set for the Oracle (§7.1).
func builtinConversationalTools() []ConversationalTool {
	return []ConversationalTool{
		{
			Name:        "file_read",
			Description: "Read a file by absolute path. Returns file contents as plain text.",
			Call: func(ctx context.Context, args map[string]any) (string, error) {
				path, ok := args["path"].(string)
				if !ok || path == "" {
					return "", fmt.Errorf("file_read: path argument is required")
				}
				// Security: reject paths with .. to prevent traversal.
				if strings.Contains(path, "..") {
					return "", fmt.Errorf("file_read: path traversal not allowed")
				}
				data, err := os.ReadFile(filepath.Clean(path))
				if err != nil {
					return "", fmt.Errorf("file_read: %w", err)
				}
				return string(data), nil
			},
		},
		{
			Name:        "code_search",
			Description: "Search for a pattern in files under a directory. Returns matching lines with file:line context.",
			Call: func(ctx context.Context, args map[string]any) (string, error) {
				pattern, ok := args["pattern"].(string)
				if !ok || pattern == "" {
					return "", fmt.Errorf("code_search: pattern argument is required")
				}
				dir, _ := args["dir"].(string)
				if dir == "" {
					dir = "."
				}
				if strings.Contains(dir, "..") {
					return "", fmt.Errorf("code_search: path traversal not allowed")
				}
				return grepFiles(ctx, dir, pattern)
			},
		},
	}
}

// grepFiles walks a directory and returns lines matching the pattern.
func grepFiles(ctx context.Context, dir, pattern string) (string, error) {
	var results []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		if info.IsDir() {
			// Skip hidden directories.
			if strings.HasPrefix(info.Name(), ".") && info.Name() != "." {
				return filepath.SkipDir
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if strings.Contains(line, pattern) {
				results = append(results, fmt.Sprintf("%s:%d: %s", path, i+1, line))
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return fmt.Sprintf("No matches found for %q in %s", pattern, dir), nil
	}
	return strings.Join(results, "\n"), nil
}
