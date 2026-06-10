// Package host — host.oracle.converse handler.
//
// Backs a free-form conversational Claude session with persistent chat
// transcript and explicit permission_mode control. Renamed from
// host.oracle.talk (oracle.go); the talk alias was removed in Phase 9.
//
// When a chat_id is provided AND a ChatStore is wired into the context,
// the handler operates in chat-aware mode: user/assistant messages are
// persisted to the transcript, and the claude session ID is stored on
// the chat row so turns resume correctly even after restarts.
//
// permission_mode controls what mutation tools the agent may execute. These
// are kitsoki-facing names translated into a real `claude` --permission-mode
// value by converseToolPolicy (the CLI rejects "ask"/"denyAll" verbatim):
//   - bypassPermissions — no enforcement (matches legacy talk behaviour); default
//   - ask              — enforcing "default" mode: the --allowedTools allowlist
//     binds and tools outside it are denied (a headless -p run can't prompt)
//   - denyAll          — enforcing mode plus a hard deny of all mutating tools
//
// background: is handled by the orchestrator, not the handler; the
// handler runs normally and the orchestrator binds the job_id when
// background: true is set on the effect.
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
	"log/slog"
	"strings"
	"time"

	"kitsoki/internal/render/sourcecolor"
	"kitsoki/internal/sysprompt"
)

// OracleBinEnv overrides the `claude` binary path for tests.
const OracleBinEnv = "KITSOKI_ORACLE_CLAUDE_BIN"

// ErrOracleUnavailable is returned when the `claude` binary is not on PATH.
var ErrOracleUnavailable = errors.New("host.oracle.converse: `claude` binary not found on PATH; install Claude Code from https://claude.ai/download")

// validPermissionModes is the set of values accepted by permission_mode:.
var validPermissionModes = map[string]bool{
	"ask":               true,
	"bypassPermissions": true,
	"denyAll":           true,
}

// OracleConverseHandler implements host.oracle.converse.
//
// Args:
//   - question        (string, required): the user's prompt for Claude
//   - chat_id         (string, required when ChatStore is available): when set
//     AND a ChatStore is in context, persists the conversation to the
//     chat transcript and reuses the claude session ID stored on the
//     chat row across turns.
//   - agent           (string, optional): name of an entry in AppDef.Agents
//     (injected via WithAgents) whose SystemPrompt is applied to this call
//     and whose Model and Tools, when non-empty, are forwarded to claude.
//   - permission_mode  (string, optional): ask | bypassPermissions | denyAll
//     (kitsoki-facing names; converseToolPolicy maps them to a CLI mode).
//     Default: bypassPermissions (matches legacy talk behaviour).
//   - working_dir     (string, optional): cwd passed to claude (scopes tool access)
//   - session_id      (string, optional, non-chat path only): UUID for session
//     persistence; if empty, a new one is generated.
//   - system_prompt   (string, optional): per-call system prompt override;
//     inline value wins over agent.SystemPrompt.
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
func OracleConverseHandler(ctx context.Context, args map[string]any) (Result, error) {
	question, _ := args["question"].(string)
	if strings.TrimSpace(question) == "" {
		return Result{Error: "host.oracle.converse: question argument is required"}, nil
	}
	// Always-on editor context: append the operator's live `/ide` selection to
	// the question so it rides the conversational turn (and is stored on the
	// chat message) exactly like the typed text. A no-op when no selection rode
	// the turn. Applied before any dispatch branch so all paths carry it.
	question = appendIDEAmbient(ctx, question)

	// B-7: If an oracle plugin registry is wired in context, route through
	// host.Dispatch. For converse the prompt is the question.
	withArgs, _ := args["with"].(map[string]any)
	if pluginRes, handled, pluginErr := TryDispatchVerb(ctx, "converse", question, "", agentNameFromArgs(args), "", withArgs, nil); handled {
		if pluginErr != nil {
			return Result{Error: pluginErr.Error()}, nil
		}
		return pluginRes, nil
	}

	permMode, _ := args["permission_mode"].(string)
	if permMode == "" {
		permMode = "bypassPermissions"
	}
	if !validPermissionModes[permMode] {
		return Result{Error: fmt.Sprintf("host.oracle.converse: unknown permission_mode %q; valid values: ask, bypassPermissions, denyAll", permMode)}, nil
	}

	chatID, _ := args["chat_id"].(string)

	if chatID != "" {
		cs := ChatStoreFromContext(ctx)
		if cs != nil {
			return runConverseWithChat(ctx, cs, chatID, question, permMode, args)
		}
		return Result{Error: "host.oracle.converse: chat_id provided but no chat store wired"}, nil
	}

	// Non-chat-aware path: stateless session.
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		sessionID = newUUID()
	}

	workingDir, _ := args["working_dir"].(string)
	agent, _ := resolveAgent(ctx, args)
	ctx, agent = applyProvider(ctx, args, agent)
	systemPrompt := effectiveSystemPrompt(args, agent)
	workingDir = appendDefaultCwd(workingDir, agent)
	tools := effectiveTools(ctx, args, agent)
	// Enforce a read-only agent's declared posture: drop bypassPermissions so
	// the allowlist binds, and hard-deny the mutating tool set. See
	// converseToolPolicy.
	permMode, disallowedTools := converseToolPolicy(permMode, agent)

	callID := newUUID()
	callStart := time.Now()
	// Install the active call_id so the claude transport tees its stream-json
	// into the agent-action-transcript sidecar keyed by this call (live path).
	ctx = WithCallID(ctx, callID)

	bin, err := resolveOracleBin(ctx)
	if err != nil {
		return Result{
			Error: err.Error(),
			Data:  map[string]any{"session_id": sessionID},
		}, nil
	}

	// Wave 3-oracle: write OracleCalled to the JSONL sink at dispatch time.
	appendOracleCalledEvent(ctx, callStart, callID, question, OracleCalledPayload{
		Verb:  "converse",
		Agent: agentNameFromArgs(args),
		Model: agent.Model,
	})

	cliArgs := []string{
		"-p",
		"--session-id", sessionID,
		"--permission-mode", permMode,
	}
	cliArgs = appendSettingSourcesFlag(cliArgs)
	cliArgs, _ = appendComposedSystemPrompt(ctx, cliArgs, sysprompt.Converse, systemPrompt, agent.InheritClaudeDefault)
	if strings.TrimSpace(agent.Model) != "" {
		cliArgs = append(cliArgs, "--model", agent.Model)
	}
	if effort := strings.TrimSpace(effectiveEffort(args, agent)); effort != "" {
		cliArgs = append(cliArgs, "--effort", effort)
	}
	cliArgs = appendAllowedToolsFlag(cliArgs, tools)
	cliArgs = appendDisallowedToolsFlag(cliArgs, disallowedTools)

	cr, _, runErr := OracleStreamer{
		Bin:        bin,
		CLIArgs:    cliArgs,
		Stdin:      question,
		WorkingDir: workingDir,
	}.Run(ctx)
	durationMS := time.Since(callStart).Milliseconds()

	if runErr != nil {
		return Result{}, runErr
	}

	converseInputDesc := map[string]any{
		"messages": []map[string]any{{"role": "user", "content": question}},
	}

	if cr.Infra != nil {
		msg := fmt.Sprintf("host.oracle.converse: claude exited with error: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		emitConverseJournal(ctx, callID, callStart, durationMS, agentNameFromArgs(args), agent.Model,
			systemPrompt, question, converseInputDesc, "", msg)
		return Result{
			Error: msg,
			Data:  map[string]any{"session_id": sessionID},
		}, nil
	}
	if cr.ExitCode != 0 {
		stderrText := strings.TrimSpace(cr.Stderr)
		msg := fmt.Sprintf("host.oracle.converse: claude exited with error: exit status %d", cr.ExitCode)
		if stderrText != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, stderrText)
		}
		emitConverseJournal(ctx, callID, callStart, durationMS, agentNameFromArgs(args), agent.Model,
			systemPrompt, question, converseInputDesc, "", msg)
		return Result{
			Error: msg,
			Data:  map[string]any{"session_id": sessionID},
		}, nil
	}

	emitConverseJournal(ctx, callID, callStart, durationMS, agentNameFromArgs(args), agent.Model,
		systemPrompt, question, converseInputDesc, cr.Stdout, "")

	return Result{
		Data: map[string]any{
			"answer":     sourcecolor.Wrap(cr.Stdout),
			"session_id": sessionID,
		},
	}, nil
}

// emitConverseJournal writes the lean slog and journal entry for converse calls.
func emitConverseJournal(ctx context.Context, callID string, callStart time.Time, durationMS int64,
	agentName, model, systemPrompt, prompt string, inputDesc map[string]any, answer, errMsg string) {

	attrs := []any{
		"call_id", callID,
		"model", model,
		"duration_ms", durationMS,
	}
	if errMsg != "" {
		attrs = append(attrs, "error", errMsg)
	}
	slog.InfoContext(ctx, "oracle.converse.complete", attrs...)

	responseDesc := map[string]any{
		"text": answer,
	}

	callEnd := time.Now()
	if errMsg != "" {
		appendOracleErrorEvent(ctx, callEnd, callID, OracleErrorPayload{
			Verb:       "converse",
			Agent:      agentName,
			DurationMS: durationMS,
			Error:      errMsg,
		})
	} else {
		appendOracleReturnedEvent(ctx, callEnd, callID, OracleReturnedPayload{
			Verb:       "converse",
			Agent:      agentName,
			Model:      model,
			DurationMS: durationMS,
			Response:   marshalResponse(responseDesc),
		})
	}
}

// runConverseWithChat executes a chat-aware converse turn: appends user/assistant
// messages to the transcript and stores the claude session ID on the chat row.
func runConverseWithChat(ctx context.Context, cs ChatStore, chatID, question, permMode string, args map[string]any) (Result, error) {
	workingDir, _ := args["working_dir"].(string)
	agent, _ := resolveAgent(ctx, args)
	ctx, agent = applyProvider(ctx, args, agent)
	systemPrompt := effectiveSystemPrompt(args, agent)
	model := agent.Model
	effort := effectiveEffort(args, agent)
	workingDir = appendDefaultCwd(workingDir, agent)
	tools := effectiveTools(ctx, args, agent)
	// Enforce a read-only agent's declared posture (see converseToolPolicy):
	// this is the path the proposal_interviewer takes (it passes a chat_id),
	// so without this a read-only discovery agent could Write to the repo.
	permMode, disallowedTools := converseToolPolicy(permMode, agent)

	var out Result
	lockErr := cs.WithLock(ctx, chatID, func(ctx context.Context) error {
		inner, runErr := doConverseChatTurn(ctx, cs, chatID, question, workingDir, systemPrompt, model, effort, permMode, tools, disallowedTools, agent.InheritClaudeDefault)
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

// doConverseChatTurn is the inner body executed inside WithLock.
//
// Step ordering: allocate/persist the Claude session ID BEFORE appending the
// user message to prevent orphan transcript rows on session-write failures.
func doConverseChatTurn(ctx context.Context, cs ChatStore, chatID, question, workingDir, systemPrompt, model, effort, permMode string, tools, disallowedTools []string, inheritDefault bool) (Result, error) {
	callID := newUUID()
	callStart := time.Now()
	// Install the active call_id so the claude transport tees its stream-json
	// into the agent-action-transcript sidecar keyed by this call (live path).
	ctx = WithCallID(ctx, callID)

	// Wave 3-oracle: write OracleCalled to the JSONL sink at dispatch time.
	appendOracleCalledEvent(ctx, callStart, callID, question, OracleCalledPayload{
		Verb:  "converse",
		Agent: "",
		Model: model,
	})

	chat, err := cs.Get(ctx, chatID)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.converse: get chat %s: %v", chatID, err)}, nil
	}

	claudeSID := chat.ClaudeSessionID
	firstTurn := claudeSID == ""
	if firstTurn {
		claudeSID = newUUID()
		if err := cs.SetClaudeSessionID(ctx, chatID, claudeSID); err != nil {
			return Result{Error: fmt.Sprintf("host.oracle.converse: set claude session id: %v", err)}, nil
		}
	}

	if _, err := cs.AppendMessage(ctx, chatID, "user", question, nil); err != nil {
		return Result{Error: fmt.Sprintf("host.oracle.converse: append user message: %v", err)}, nil
	}

	bin, err := resolveOracleBin(ctx)
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
		"--permission-mode", permMode,
	}
	cliArgs = appendSettingSourcesFlag(cliArgs)
	if firstTurn {
		cliArgs = append(cliArgs, "--session-id", claudeSID)
	} else {
		cliArgs = append(cliArgs, "--resume", claudeSID)
	}
	cliArgs, _ = appendComposedSystemPrompt(ctx, cliArgs, sysprompt.Converse, systemPrompt, inheritDefault)
	if strings.TrimSpace(model) != "" {
		cliArgs = append(cliArgs, "--model", model)
	}
	if e := strings.TrimSpace(effort); e != "" {
		cliArgs = append(cliArgs, "--effort", e)
	}
	cliArgs = appendAllowedToolsFlag(cliArgs, tools)
	cliArgs = appendDisallowedToolsFlag(cliArgs, disallowedTools)

	cr, _, runErr := OracleStreamer{
		Bin:        bin,
		CLIArgs:    cliArgs,
		Stdin:      question,
		WorkingDir: workingDir,
	}.Run(ctx)
	durationMS := time.Since(callStart).Milliseconds()

	if runErr != nil {
		return Result{}, runErr
	}

	chatInputDesc := map[string]any{
		"messages": []map[string]any{{"role": "user", "content": question}},
	}

	if cr.Infra != nil {
		msg := fmt.Sprintf("host.oracle.converse: claude exec failed: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		emitConverseJournal(ctx, callID, callStart, durationMS, "", model, systemPrompt, question, chatInputDesc, "", msg)
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
		emitConverseJournal(ctx, callID, callStart, durationMS, "", model, systemPrompt, question, chatInputDesc, "", msg)
		return Result{
			Error: msg,
			Data: map[string]any{
				"session_id":        claudeSID,
				"chat_id":           chatID,
				"claude_session_id": claudeSID,
			},
		}, nil
	}

	_, appendErr := cs.AppendMessage(ctx, chatID, "assistant", cr.Stdout, map[string]any{
		"exit_code": cr.ExitCode,
	})
	if appendErr != nil {
		emitConverseJournal(ctx, callID, callStart, durationMS, "", model, systemPrompt, question, chatInputDesc, cr.Stdout,
			fmt.Sprintf("host.oracle.converse: persist assistant message: %v", appendErr))
		return Result{
			Error: fmt.Sprintf("host.oracle.converse: persist assistant message: %v", appendErr),
			Data: map[string]any{
				"answer":            sourcecolor.Wrap(cr.Stdout),
				"session_id":        claudeSID,
				"chat_id":           chatID,
				"claude_session_id": claudeSID,
			},
		}, nil
	}

	emitConverseJournal(ctx, callID, callStart, durationMS, "", model, systemPrompt, question, chatInputDesc, cr.Stdout, "")

	seq, _ := cs.LatestSeq(ctx, chatID)

	return Result{Data: map[string]any{
		"answer":            sourcecolor.Wrap(cr.Stdout),
		"session_id":        claudeSID,
		"chat_id":           chatID,
		"claude_session_id": claudeSID,
		"transcript_seq":    seq,
	}}, nil
}

// RenderConverseSpan renders a converse span as the opaque block format
// (decision D10: converse spans render as opaque blocks). Replay tooling
// calls this instead of re-running the conversation — conversations are
// the artifact, not a replayable sequence.
//
// Format: converse(chat=<chatID>, seq=[<seqStart>..<seqEnd>]) — <turns> turns, see ChatStore
func RenderConverseSpan(chatID string, seqStart, seqEnd int) string {
	turns := seqEnd - seqStart
	return fmt.Sprintf("converse(chat=%s, seq=[%d..%d]) — %d turns, see ChatStore",
		chatID, seqStart, seqEnd, turns)
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
