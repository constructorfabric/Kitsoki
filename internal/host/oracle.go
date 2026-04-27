// Package host — host.oracle.talk handler for conversational Claude sessions.
//
// Backs a conversational Claude-Code-clone session via `claude -p`. The session
// ID is round-tripped through args/result so the calling state can persist it
// in world and resume across turns (and across room exits/re-entries).
//
// For one-shot Claude calls driven by a prompt file, see host.oracle.ask
// (oracle_ask.go).
package host

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// OracleBinEnv overrides the `claude` binary path for tests.
const OracleBinEnv = "HALLY_ORACLE_CLAUDE_BIN"

// ErrOracleUnavailable is returned when the `claude` binary is not on PATH.
var ErrOracleUnavailable = errors.New("host.oracle.talk: `claude` binary not found on PATH; install Claude Code from https://claude.ai/download")

// OracleTalkHandler implements host.oracle.talk.
//
// Args:
//   - question (string, required): the user's prompt for Claude
//   - session_id (string, optional): UUID for session persistence; if empty, a new one is generated
//   - working_dir (string, optional): cwd passed to claude (scopes tool access)
//
// Returns Result.Data with:
//   - answer (string): Claude's text reply
//   - session_id (string): the session ID used (new or echoed back)
//
// If the claude binary is unavailable, returns Result{Error: ...} rather than
// a Go error so app flow tests continue to pass; the state machine surfaces
// the error via on_error:.
func OracleTalkHandler(ctx context.Context, args map[string]any) (Result, error) {
	question, _ := args["question"].(string)
	if strings.TrimSpace(question) == "" {
		return Result{Error: "host.oracle.talk: question argument is required"}, nil
	}

	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		sessionID = newUUID()
	}

	workingDir, _ := args["working_dir"].(string)

	bin := os.Getenv(OracleBinEnv)
	if bin == "" {
		path, err := exec.LookPath("claude")
		if err != nil {
			return Result{
				Error: ErrOracleUnavailable.Error(),
				Data: map[string]any{
					"session_id": sessionID,
				},
			}, nil
		}
		bin = path
	}

	cliArgs := []string{
		"-p",
		"--session-id", sessionID,
		"--output-format", "text",
		"--permission-mode", "bypassPermissions",
	}

	cmd := exec.CommandContext(ctx, bin, cliArgs...)
	cmd.Stdin = strings.NewReader(question)
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		stderrText := strings.TrimSpace(stderr.String())
		msg := fmt.Sprintf("host.oracle.talk: claude exited with error: %v", err)
		if stderrText != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, stderrText)
		}
		return Result{
			Error: msg,
			Data: map[string]any{
				"session_id": sessionID,
			},
		}, nil
	}

	answer := strings.TrimRight(stdout.String(), "\n")
	return Result{
		Data: map[string]any{
			"answer":     answer,
			"session_id": sessionID,
		},
	}, nil
}

// newUUID returns a v4-style UUID string. Uses crypto/rand so it's unique
// across sessions and safe for concurrent use.
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should not fail on Linux; fall back to a zeroed UUID
		// rather than panicking in a library call.
		return "00000000-0000-0000-0000-000000000000"
	}
	// RFC 4122 v4 bits.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32])
}
