// Package host — shared helper for materialising a prompt body to a
// tempfile so handlers that read `prompt_path:` (notably
// host.agent.ask_with_mcp) can be reused by callers that already have the
// rendered prompt as an in-memory string.
//
// Lifted from internal/metamode/adapter.go (WS-A3): both sites need the
// same "write a body to a tempfile, return the path, defer cleanup"
// trick. The metamode-side composes (SystemPrompt + separator + UserMsg)
// before writing; the WS-A7 per-call `agent:` path writes the agent's
// SystemPrompt alone. Both call into writePromptTempFile.
package host

import (
	"fmt"
	"os"
)

// WritePromptTempFile writes body to a kitsoki-prompt-*.txt tempfile and
// returns its absolute path together with a cleanup function the caller
// MUST defer. On any write error the function cleans up after itself and
// returns the error.
//
// Exported so internal/metamode/adapter.go can share the same mechanics
// for its system-prompt + user-message composition path.
func WritePromptTempFile(body string) (path string, cleanup func(), err error) {
	f, ferr := os.CreateTemp("", "kitsoki-prompt-*.txt")
	if ferr != nil {
		return "", func() {}, fmt.Errorf("create prompt tempfile: %w", ferr)
	}
	if _, werr := f.WriteString(body); werr != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("write prompt tempfile: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(f.Name())
		return "", func() {}, fmt.Errorf("close prompt tempfile: %w", cerr)
	}
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}
