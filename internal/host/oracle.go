// Package host — host.oracle.talk handler for conversational Claude sessions.
//
// Backs a conversational Claude-Code-clone session via `claude -p`. The session
// ID is round-tripped through args/result so the calling state can persist it
// in world and resume across turns (and across room exits/re-entries).
//
// When a chat_id is provided AND a ChatStore is wired into the context, the
// handler operates in chat-aware mode: user/assistant messages are persisted to
// the transcript, and the claude session ID is stored on the chat row so turns
// resume correctly even after restarts.
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
const OracleBinEnv = "KITSOKI_ORACLE_CLAUDE_BIN"

// ErrOracleUnavailable is returned when the `claude` binary is not on PATH.
var ErrOracleUnavailable = errors.New("host.oracle.talk: `claude` binary not found on PATH; install Claude Code from https://claude.ai/download")

// OracleTalkHandler implements host.oracle.talk.
//
// Args:
//   - question    (string, required): the user's prompt for Claude
//   - session_id  (string, optional): UUID for session persistence; if empty, a new one is generated
//   - working_dir (string, optional): cwd passed to claude (scopes tool access)
//   - chat_id     (string, optional): when set AND a ChatStore is in context,
//     persists the conversation to the chat transcript and reuses the claude
//     session ID stored on the chat row across turns.
//
// Returns Result.Data with:
//   - answer             (string): Claude's text reply
//   - session_id         (string): the session ID used (new or echoed back)
//   - chat_id            (string, chat-aware path only)
//   - claude_session_id  (string, chat-aware path only): same as session_id
//   - transcript_seq     (int, chat-aware path only): seq of the assistant message
//
// If the claude binary is unavailable, returns Result{Error: ...} rather than
// a Go error so app flow tests continue to pass; the state machine surfaces
// the error via on_error:.
func OracleTalkHandler(ctx context.Context, args map[string]any) (Result, error) {
	question, _ := args["question"].(string)
	if strings.TrimSpace(question) == "" {
		return Result{Error: "host.oracle.talk: question argument is required"}, nil
	}

	chatID, _ := args["chat_id"].(string)

	// Chat-aware path: chat_id provided AND ChatStore available.
	if chatID != "" {
		cs := ChatStoreFromContext(ctx)
		if cs != nil {
			return runOracleTalkWithChat(ctx, cs, chatID, question, args)
		}
		// chat_id requested but no store — surface as a domain error so
		// on_error: routing handles it.
		return Result{Error: "host.oracle.talk: chat_id provided but no chat store wired"}, nil
	}

	// Legacy path (unchanged) — no chat persistence.
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

// runOracleTalkWithChat executes a chat-aware oracle turn: appends user/assistant
// messages to the transcript and stores the claude session ID on the chat row.
func runOracleTalkWithChat(ctx context.Context, cs ChatStore, chatID, question string, args map[string]any) (Result, error) {
	workingDir, _ := args["working_dir"].(string)

	var out Result
	lockErr := cs.WithLock(ctx, chatID, func(ctx context.Context) error {
		inner, runErr := doOracleChatTurn(ctx, cs, chatID, question, workingDir)
		out = inner
		return runErr
	})
	if errors.Is(lockErr, ErrChatBusy) {
		return Result{Error: lockErr.Error()}, nil
	}
	if lockErr != nil {
		return Result{}, lockErr
	}
	return out, nil
}

// doOracleChatTurn is the inner body executed inside WithLock.
//
// Step ordering matters: we must allocate/persist the Claude session ID
// BEFORE we append the user message, otherwise a SetClaudeSessionID failure
// leaves an orphan user message in the transcript with no claude session to
// resume. See I10 in the agent-rooms review.
func doOracleChatTurn(ctx context.Context, cs ChatStore, chatID, question, workingDir string) (Result, error) {
	chat, err := cs.Get(ctx, chatID)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.talk: get chat %s: %v", chatID, err)}, nil
	}

	// Determine/assign claude session ID FIRST. If the SetClaudeSessionID
	// write fails we bail before mutating the transcript.
	claudeSID := chat.ClaudeSessionID
	if claudeSID == "" {
		claudeSID = newUUID()
		if err := cs.SetClaudeSessionID(ctx, chatID, claudeSID); err != nil {
			return Result{Error: fmt.Sprintf("host.oracle.talk: set claude session id: %v", err)}, nil
		}
	}

	// Append user message. With the session ID already persisted, a failure
	// here doesn't strand an unanswered user turn against a session-less chat.
	if _, err := cs.AppendMessage(ctx, chatID, "user", question, nil); err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.talk: append user message: %v", err)}, nil
	}

	bin, err := resolveOracleBin()
	if err != nil {
		return Result{
			Error: err.Error(),
			Data: map[string]any{
				"session_id":        claudeSID,
				"chat_id":           chatID,
				"claude_session_id": claudeSID,
			},
		}, nil
	}

	cliArgs := []string{
		"-p",
		"--session-id", claudeSID,
		"--output-format", "text",
		"--permission-mode", "bypassPermissions",
	}

	cr, runErr := runClaudeOneShot(ctx, bin, cliArgs, question, workingDir)
	if runErr != nil {
		return Result{}, runErr
	}

	if cr.Infra != nil {
		msg := fmt.Sprintf("host.oracle.talk: claude exec failed: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		return Result{
			Error: msg,
			Data: map[string]any{
				"session_id":        claudeSID,
				"chat_id":           chatID,
				"claude_session_id": claudeSID,
			},
		}, nil
	}

	if cr.ExitCode != 0 {
		msg := claudeExitErrorMessage(cr.ExitCode, cr.Stderr, cr.Stdout)
		return Result{
			Error: msg,
			Data: map[string]any{
				"session_id":        claudeSID,
				"chat_id":           chatID,
				"claude_session_id": claudeSID,
			},
		}, nil
	}

	// Append assistant message. Surface persistence failures via Result.Error
	// so on_error: routing fires (consistent with how lock-busy is reported).
	// The user already has the answer in stdout — we still expose it under
	// Result.Data["answer"] for diagnostic purposes; the failure mode here
	// is "the answer is correct but won't be in the transcript next turn".
	_, appendErr := cs.AppendMessage(ctx, chatID, "assistant", cr.Stdout, map[string]any{
		"exit_code": cr.ExitCode,
	})
	if appendErr != nil {
		return Result{
			Error: fmt.Sprintf("host.oracle.talk: persist assistant message: %v", appendErr),
			Data: map[string]any{
				"answer":            cr.Stdout,
				"session_id":        claudeSID,
				"chat_id":           chatID,
				"claude_session_id": claudeSID,
			},
		}, nil
	}

	seq, _ := cs.LatestSeq(ctx, chatID)

	return Result{Data: map[string]any{
		"answer":            cr.Stdout,
		"session_id":        claudeSID,
		"chat_id":           chatID,
		"claude_session_id": claudeSID,
		"transcript_seq":    seq,
	}}, nil
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
