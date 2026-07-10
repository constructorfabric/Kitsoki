// Package host — host.agent.converse handler.
//
// Backs a free-form conversational Claude session with persistent chat
// transcript and explicit permission_mode control. Renamed from
// host.agent.talk (agent.go); the talk alias was removed in Phase 9.
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
// For one-shot Claude calls driven by a prompt file, see host.agent.ask
// (agent_ask.go).
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

// AgentBinEnv overrides the `claude` binary path for tests.
const AgentBinEnv = "KITSOKI_AGENT_CLAUDE_BIN"

// ErrAgentUnavailable is returned when the `claude` binary is not on PATH.
var ErrAgentUnavailable = errors.New("host.agent.converse: `claude` binary not found on PATH; install Claude Code from https://claude.ai/download")

// validPermissionModes is the set of values accepted by permission_mode:.
var validPermissionModes = map[string]bool{
	"ask":               true,
	"bypassPermissions": true,
	"denyAll":           true,
}

// AgentConverseHandler implements host.agent.converse.
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
func AgentConverseHandler(ctx context.Context, args map[string]any) (Result, error) {
	question, _ := args["question"].(string)
	if strings.TrimSpace(question) == "" {
		return Result{Error: "host.agent.converse: question argument is required"}, nil
	}
	// Always-on editor context: append the operator's live `/ide` selection to
	// the question so it rides the conversational turn (and is stored on the
	// chat message) exactly like the typed text. A no-op when no selection rode
	// the turn. Applied before any dispatch branch so all paths carry it.
	question = appendIDEAmbient(ctx, question)
	// Always-on screen context: append the operator's pointed-at element/frame
	// (a web/tui surface attached it via WithVisualAmbient) beside the editor
	// selection. A no-op when no surface attached a bundle. See visual_ambient.go.
	question = appendVisualAmbient(ctx, question)

	sandbox, sandboxErr := parseAgentSandbox(args)
	if sandboxErr != "" {
		return Result{Error: "host.agent.converse: " + sandboxErr}, nil
	}
	policyWorkingDir, _ := args["working_dir"].(string)
	if agent, ok := resolveAgent(ctx, args); ok {
		policyWorkingDir = appendDefaultCwd(policyWorkingDir, agent)
	}
	if _, policyErr := RequireAgentLaunchAllowed(ctx, "converse", agentNameFromArgs(args), policyWorkingDir); policyErr != "" {
		return Result{Error: "host.agent.converse: " + policyErr}, nil
	}

	// B-7: If an agent plugin registry is wired in context, route through
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
		if p, ok := args["permissions"].(map[string]any); ok {
			permMode, _ = p["mode"].(string)
		}
	}
	if permMode == "" {
		if a, ok := resolveAgent(ctx, args); ok {
			permMode = a.Permissions.Mode
		}
	}
	if permMode == "" {
		permMode = "bypassPermissions"
	}
	if !validPermissionModes[permMode] {
		return Result{Error: fmt.Sprintf("host.agent.converse: unknown permission_mode %q; valid values: ask, bypassPermissions, denyAll", permMode)}, nil
	}

	chatID, _ := args["chat_id"].(string)

	if chatID != "" {
		cs := ChatStoreFromContext(ctx)
		if cs != nil {
			return runConverseWithChat(ctx, cs, chatID, question, permMode, args)
		}
		return Result{Error: "host.agent.converse: chat_id provided but no chat store wired"}, nil
	}

	// Non-chat-aware path: stateless session.
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		sessionID = newUUID()
	}

	workingDir, _ := args["working_dir"].(string)
	agent, _ := resolveAgent(ctx, args)
	ctx, agent = applyProvider(ctx, args, agent)
	systemPrompt := converseSystemPrompt(args, agent)
	workingDir = appendDefaultCwd(workingDir, agent)
	policy := enforceToolbox(ctx, args, agent, permMode)
	permMode = policy.CLIMode
	tools := policy.AllowedTools
	disallowedTools := policy.DeniedTools

	callID := newUUID()
	callStart := time.Now()
	// Install the active call_id so the claude transport tees its stream-json
	// into the agent-action-transcript sidecar keyed by this call (live path).
	ctx = WithCallID(ctx, callID)

	bin, err := resolveAgentBin(ctx)
	if err != nil {
		return Result{
			Error: err.Error(),
			Data:  map[string]any{"session_id": sessionID},
		}, nil
	}

	// Record the operator's screen-context bundle (slice 1's VisualAmbient) as
	// the call's `input.visual` — the auditable INPUT to the decision (frame by
	// handle, point, resolved element + bbox). Reject up front a frame_handle
	// that does not resolve to a recorded artifact (no dangling frame reference).
	visualBlock, hasVisual, visualErr := recordedVisualInput(ctx)
	if visualErr != nil {
		return Result{
			Error: fmt.Sprintf("host.agent.converse: %v", visualErr),
			Data:  map[string]any{"session_id": sessionID},
		}, nil
	}
	calledPayload := AgentCalledPayload{
		Verb:  "converse",
		Agent: agentNameFromArgs(args),
		Model: agent.Model,
	}
	if hasVisual {
		calledPayload.Input = marshalInput(map[string]any{
			"messages": []map[string]any{{"role": "user", "content": question}},
			"visual":   visualBlock,
		})
	}

	// Wave 3-agent: write AgentCalled to the JSONL sink at dispatch time.
	appendAgentCalledEvent(ctx, callStart, callID, question, policy.AgentCalledFields(calledPayload))

	cliArgs := []string{
		"-p",
		"--session-id", sessionID,
		"--permission-mode", permMode,
	}
	cliArgs = appendSettingSourcesFlag(cliArgs)
	cliArgs = appendDisableSlashCommandsFlag(cliArgs)
	cliArgs = appendStrictMCPConfigFlag(cliArgs)
	cliArgs, _ = appendComposedSystemPrompt(ctx, cliArgs, sysprompt.Converse, systemPrompt, agent.InheritClaudeDefault)
	if strings.TrimSpace(agent.Model) != "" {
		cliArgs = append(cliArgs, "--model", agent.Model)
	}
	if effort := strings.TrimSpace(effectiveEffort(args, agent)); effort != "" {
		cliArgs = append(cliArgs, "--effort", effort)
	}
	// Forward operator questions into kitsoki when a live surface is attached.
	var opAskCleanup func()
	cliArgs, tools, opAskCleanup, _ = attachOperatorAsk(ctx, cliArgs, tools)
	defer opAskCleanup()
	policy = policy.WithAllowed(tools)
	if contractServers := attachStudioMCPServer(effectiveMCPServers(args, agent), tools); len(contractServers) > 0 {
		contractMCPPath, contractMCPCleanup, mErr := writeMCPConfigTempfile(contractServers, "kitsoki-converse-contract-mcp")
		if mErr != nil {
			return Result{Error: fmt.Sprintf("host.agent.converse: write contract MCP config: %v", mErr)}, nil
		}
		defer contractMCPCleanup()
		cliArgs = append(cliArgs, "--mcp-config", contractMCPPath)
	}
	cliArgs = appendAllowedToolsFlag(cliArgs, tools)
	cliArgs = appendDisallowedToolsFlag(cliArgs, append(disallowedTools, effectiveDisallowedTools(args, agent)...))

	cr, _, runErr := AgentStreamer{
		Bin:        bin,
		CLIArgs:    cliArgs,
		Stdin:      question,
		WorkingDir: workingDir,
		Sandbox:    sandbox,
	}.Run(ctx)
	durationMS := time.Since(callStart).Milliseconds()

	if runErr != nil {
		return Result{}, runErr
	}

	converseInputDesc := map[string]any{
		"messages": []map[string]any{{"role": "user", "content": question}},
	}

	if cr.Infra != nil {
		msg := fmt.Sprintf("host.agent.converse: claude exited with error: %v", cr.Infra)
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
		msg := fmt.Sprintf("host.agent.converse: claude exited with error: exit status %d", cr.ExitCode)
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
	slog.InfoContext(ctx, "agent.converse.complete", attrs...)

	responseDesc := map[string]any{
		"text": answer,
	}

	callEnd := time.Now()
	if errMsg != "" {
		appendAgentErrorEvent(ctx, callEnd, callID, AgentErrorPayload{
			Verb:       "converse",
			Agent:      agentName,
			DurationMS: durationMS,
			Error:      errMsg,
		})
	} else {
		appendAgentReturnedEvent(ctx, callEnd, callID, AgentReturnedPayload{
			Verb:       "converse",
			Agent:      agentName,
			Model:      model,
			DurationMS: durationMS,
			Response:   marshalResponse(responseDesc),
		})
	}
}

// converseSystemPrompt resolves the effective system prompt for a converse
// call and appends the engine-composed `room_context` arg when present —
// the orchestrator's off-ramp / conversation lane passes the resting room's
// purpose, available commands, and relevant world through it so the agent
// starts every turn already oriented (see
// internal/orchestrator/offramp_conversation.go). Appended per call (it
// rides --append-system-prompt / the composed prompt, not the transcript),
// so a resumed chat always sees the CURRENT room state, not a stale copy.
func converseSystemPrompt(args map[string]any, agent Agent) string {
	sp := effectiveSystemPrompt(args, agent)
	rc, _ := args["room_context"].(string)
	if strings.TrimSpace(rc) == "" {
		return sp
	}
	if strings.TrimSpace(sp) == "" {
		return rc
	}
	return sp + "\n\n" + rc
}

// runConverseWithChat executes a chat-aware converse turn: appends user/assistant
// messages to the transcript and stores the claude session ID on the chat row.
func runConverseWithChat(ctx context.Context, cs ChatStore, chatID, question, permMode string, args map[string]any) (Result, error) {
	workingDir, _ := args["working_dir"].(string)
	agent, _ := resolveAgent(ctx, args)
	ctx, agent = applyProvider(ctx, args, agent)
	systemPrompt := converseSystemPrompt(args, agent)
	model := agent.Model
	effort := effectiveEffort(args, agent)
	workingDir = appendDefaultCwd(workingDir, agent)
	policy := enforceToolbox(ctx, args, agent, permMode)
	permMode = policy.CLIMode
	tools := policy.AllowedTools
	disallowedTools := policy.DeniedTools

	var out Result
	lockErr := cs.WithLock(ctx, chatID, func(ctx context.Context) error {
		sandbox, sandboxErr := parseAgentSandbox(args)
		if sandboxErr != "" {
			out = Result{Error: "host.agent.converse: " + sandboxErr}
			return nil
		}
		inner, runErr := doConverseChatTurn(ctx, cs, chatID, question, workingDir, systemPrompt, model, effort, permMode, tools, disallowedTools, agent.InheritClaudeDefault, policy, sandbox)
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
func doConverseChatTurn(ctx context.Context, cs ChatStore, chatID, question, workingDir, systemPrompt, model, effort, permMode string, tools, disallowedTools []string, inheritDefault bool, policy ToolboxEnforcement, sandbox *AgentSandboxSpec) (Result, error) {
	callID := newUUID()
	callStart := time.Now()
	// Install the active call_id so the claude transport tees its stream-json
	// into the agent-action-transcript sidecar keyed by this call (live path).
	ctx = WithCallID(ctx, callID)

	// Record the operator's screen-context bundle as the call's `input.visual`
	// (see the non-chat path). Reject a dangling frame_handle before any
	// transcript row is written so a failed-validation turn leaves no orphan.
	visualBlock, hasVisual, visualErr := recordedVisualInput(ctx)
	if visualErr != nil {
		return Result{Error: fmt.Sprintf("host.agent.converse: %v", visualErr)}, nil
	}
	calledPayload := AgentCalledPayload{
		Verb:  "converse",
		Agent: "",
		Model: model,
	}
	if hasVisual {
		calledPayload.Input = marshalInput(map[string]any{
			"messages": []map[string]any{{"role": "user", "content": question}},
			"visual":   visualBlock,
		})
	}

	// Wave 3-agent: write AgentCalled to the JSONL sink at dispatch time.
	appendAgentCalledEvent(ctx, callStart, callID, question, policy.AgentCalledFields(calledPayload))

	chat, err := cs.GetOrEnsure(ctx, chatID)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.agent.converse: get chat %s: %v", chatID, err)}, nil
	}

	claudeSID := chat.ClaudeSessionID
	firstTurn := claudeSID == ""
	if firstTurn {
		claudeSID = newUUID()
		if err := cs.SetClaudeSessionID(ctx, chatID, claudeSID); err != nil {
			return Result{Error: fmt.Sprintf("host.agent.converse: set claude session id: %v", err)}, nil
		}
	}

	if _, err := cs.AppendMessage(ctx, chatID, "user", question, nil); err != nil {
		return Result{Error: fmt.Sprintf("host.agent.converse: append user message: %v", err)}, nil
	}

	bin, err := resolveAgentBin(ctx)
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
	cliArgs = appendDisableSlashCommandsFlag(cliArgs)
	cliArgs = appendStrictMCPConfigFlag(cliArgs)
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
	// This chat-aware path builds no other --mcp-config (unlike the
	// stateless path above), so a chat agent whose tool surface names a
	// studio tool needs its own attach here rather than riding along on
	// attachOperatorAsk's config (which only exists when an operator is
	// attached).
	if studioServers := attachStudioMCPServer(nil, tools); len(studioServers) > 0 {
		studioMCPPath, studioMCPCleanup, mErr := writeMCPConfigTempfile(studioServers, "kitsoki-converse-chat-studio-mcp")
		if mErr != nil {
			return Result{Error: fmt.Sprintf("host.agent.converse: write studio MCP config: %v", mErr)}, nil
		}
		defer studioMCPCleanup()
		cliArgs = append(cliArgs, "--mcp-config", studioMCPPath)
	}
	// Forward operator questions into kitsoki when a live surface is attached.
	var opAskCleanup func()
	cliArgs, tools, opAskCleanup, _ = attachOperatorAsk(ctx, cliArgs, tools)
	defer opAskCleanup()
	policy = policy.WithAllowed(tools)
	cliArgs = appendAllowedToolsFlag(cliArgs, tools)
	cliArgs = appendDisallowedToolsFlag(cliArgs, disallowedTools)

	cr, returnedSID, runErr := AgentStreamer{
		Bin:        bin,
		CLIArgs:    cliArgs,
		Stdin:      question,
		WorkingDir: workingDir,
		Sandbox:    sandbox,
	}.Run(ctx)
	durationMS := time.Since(callStart).Milliseconds()

	if runErr != nil {
		return Result{}, runErr
	}

	// Self-heal a stale/foreign resume id. A chat may hold a session id the
	// active backend can't resume — most commonly a codex chat whose stored id
	// was a pre-mint uuid codex never created, so `exec resume <uuid>` fails with
	// "thread/resume … no rollout found". Rather than wedge on every turn, mint a
	// fresh session and retry ONCE without --resume so the chat recovers; the
	// persisted id is then corrected to whatever the backend actually mints.
	if !firstTurn && cr.ExitCode != 0 &&
		(strings.Contains(cr.Stderr+cr.Stdout, "no rollout found") ||
			strings.Contains(cr.Stderr+cr.Stdout, "thread/resume")) {
		fresh := newUUID()
		if err := cs.SetClaudeSessionID(ctx, chatID, fresh); err == nil {
			retry := make([]string, 0, len(cliArgs))
			for i := 0; i < len(cliArgs); i++ {
				if cliArgs[i] == "--resume" { // drop the flag and its value
					i++
					continue
				}
				retry = append(retry, cliArgs[i])
			}
			retry = append(retry, "--session-id", fresh)
			claudeSID = fresh
			cr, returnedSID, runErr = AgentStreamer{
				Bin:        bin,
				CLIArgs:    retry,
				Stdin:      question,
				WorkingDir: workingDir,
				Sandbox:    sandbox,
			}.Run(ctx)
			durationMS = time.Since(callStart).Milliseconds()
			if runErr != nil {
				return Result{}, runErr
			}
		}
	}

	// Persist the backend's ACTUAL session id when it differs from the one we
	// passed. codex ignores our pre-minted --session-id and mints its own
	// thread_id (surfaced via thread.started); without persisting it, the next
	// turn's `--resume <our-uuid>` targets a thread codex never created and
	// fails with "thread/resume … no rollout found for thread id <uuid>". The
	// streamer extracts the real id; adopt it so subsequent resumes hit the
	// thread the backend actually owns. (For claude this is a no-op — it honors
	// the id we passed, so returnedSID == claudeSID.)
	if returnedSID != "" && returnedSID != claudeSID {
		if err := cs.SetClaudeSessionID(ctx, chatID, returnedSID); err == nil {
			claudeSID = returnedSID
		}
	}

	chatInputDesc := map[string]any{
		"messages": []map[string]any{{"role": "user", "content": question}},
	}

	if cr.Infra != nil {
		msg := fmt.Sprintf("host.agent.converse: claude exec failed: %v", cr.Infra)
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
			fmt.Sprintf("host.agent.converse: persist assistant message: %v", appendErr))
		return Result{
			Error: fmt.Sprintf("host.agent.converse: persist assistant message: %v", appendErr),
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
