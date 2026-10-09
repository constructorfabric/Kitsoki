// Package host — host.agent.ask_with_mcp handler for one-shot Claude calls
// that need MCP servers attached (typed JSON via wiggum-style schema validators
// being the primary use-case).
//
// This is host.agent.ask plus an mcp_servers: arg that is materialized into a
// temporary --mcp-config file and passed to `claude -p`. The bug-fix room
// uses this for every LLM-driven phase.
//
// Field naming note (N11): the canonical text-output field on this handler
// is `stdout` (consistent with the rest of host.run / host.agent.* surface).
// In the chat-aware path we additionally expose the same string under the
// alias `answer` so YAML can use a single `answer` key whether the upstream
// handler is host.agent.talk (which only has `answer`) or
// host.agent.ask_with_mcp. We do NOT rename `stdout` — established callers
// (validator path, non-chat path, on_complete notification) still bind it.
package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	kitsokimcp "kitsoki/internal/mcp"
	"kitsoki/internal/render/sourcecolor"
	"kitsoki/internal/storyauthoring"
	"kitsoki/internal/sysprompt"
)

// kitsokiBinaryEnv overrides the path to the kitsoki binary used to spawn the
// auto-attached validator. Set in tests; in production callers may also
// set it if `kitsoki` is not the running binary's name.
const kitsokiBinaryEnv = "KITSOKI_BIN"

// postCmdArgKeyRe constrains post_cmd_args keys so each renders to exactly one
// `--<Key>` argv slot in the validator subprocess. A key with spaces (or other
// metacharacters) would otherwise create stray argv slots.
var postCmdArgKeyRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// validatorOptions bundles the optional validator configuration plumbed
// through from a `validator:` sub-block on the YAML `with:` map. All
// fields are optional; when the entire validator: block is absent the
// caller passes a zero value and the validator runs schema-only as
// before. Templating of post_cmd_args (`{{ world.X }}`) is handled
// up-stream by the orchestrator's RawWith re-render machinery — by the
// time these strings reach the handler they are already resolved.
type validatorOptions struct {
	// PostCmd is the verifier command (e.g. "python3 -m bugfix verify-impl").
	// Empty = schema-only.
	PostCmd string
	// PostCmdArgs is an ordered list of key=value pairs forwarded to the
	// post-cmd subprocess as `--key value`. Order is preserved so argv
	// composition is deterministic across iterations.
	PostCmdArgs []postCmdKV
	// PostCmdCwd is the working directory for the verifier subprocess
	// (relative paths resolved against KITSOKI_APP_DIR via resolvePromptPath).
	// Empty = inherit kitsoki's cwd.
	PostCmdCwd string
	// MaxRetries caps the inner submit-retry budget. Zero = let the
	// validator pick its default (5).
	MaxRetries int
	// StateFilePath, when non-empty, is forwarded to the validator's
	// --state-file flag. The host handler creates the path itself when
	// running multi-iteration retry loops so counters span re-engagements.
	StateFilePath string
}

// postCmdKV is a key/value pair forwarded as `--<key> <value>` to the
// validator's --post-cmd subprocess.
type postCmdKV struct {
	Key   string
	Value string
}

// parseValidatorOptions extracts a `validator:` sub-block from the
// handler's call args. Returns the zero value when the block is absent,
// or an error message when fields are present but malformed (the
// caller surfaces these as Result.Error so on_error: can route).
//
// Expected YAML shape:
//
//	validator:
//	  post_cmd: "python3 -m bugfix verify-impl"
//	  post_cmd_args:
//	    ticket: "{{ world.ticket }}"
//	    worktree: ".bug-fix/{{ world.ticket }}/worktree"
//	  post_cmd_cwd: "tools/loopy"
//	  max_retries: 5
//
// post_cmd_args is a map; iteration order through Go maps is non-
// deterministic but the resulting argv is sorted by key so the
// validator subprocess sees a stable argv across iterations.
func parseValidatorOptions(args map[string]any) (validatorOptions, string) {
	rawBlock, ok := args["validator"]
	if !ok || rawBlock == nil {
		return validatorOptions{}, ""
	}
	blk, ok := rawBlock.(map[string]any)
	if !ok {
		return validatorOptions{}, "validator: must be a mapping"
	}

	var opts validatorOptions
	if v, _ := blk["post_cmd"].(string); strings.TrimSpace(v) != "" {
		opts.PostCmd = v
	}
	if v, _ := blk["post_cmd_cwd"].(string); strings.TrimSpace(v) != "" {
		opts.PostCmdCwd = v
	}
	switch v := blk["max_retries"].(type) {
	case int:
		opts.MaxRetries = v
	case int8:
		opts.MaxRetries = int(v)
	case int16:
		opts.MaxRetries = int(v)
	case int32:
		opts.MaxRetries = int(v)
	case int64:
		opts.MaxRetries = int(v)
	case uint:
		opts.MaxRetries = int(v)
	case uint8:
		opts.MaxRetries = int(v)
	case uint16:
		opts.MaxRetries = int(v)
	case uint32:
		opts.MaxRetries = int(v)
	case uint64:
		// goccy/go-yaml stores positive YAML integers as uint64 in
		// IntegerNode.Value (see ast.IntegerNode godoc: "int64 or uint64").
		// `max_retries: 5` arrives here as uint64.
		opts.MaxRetries = int(v)
	case float32:
		opts.MaxRetries = int(v)
	case float64:
		// YAML ints often arrive as float64 through JSON-shaped paths.
		opts.MaxRetries = int(v)
	case nil:
		// absent — leave as zero
	default:
		return validatorOptions{}, fmt.Sprintf("validator.max_retries: must be a number (got %T)", v)
	}

	if rawArgs, present := blk["post_cmd_args"]; present && rawArgs != nil {
		argsMap, ok := rawArgs.(map[string]any)
		if !ok {
			return validatorOptions{}, "validator.post_cmd_args: must be a mapping of string→string"
		}
		// Sort keys so argv composition is deterministic across iterations
		// (matters for the state-file resume path: identical claude
		// invocations across restarts).
		keys := make([]string, 0, len(argsMap))
		for k := range argsMap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			val, ok := argsMap[k].(string)
			if !ok {
				return validatorOptions{}, fmt.Sprintf("validator.post_cmd_args[%q]: must be a string (got %T)", k, argsMap[k])
			}
			if !postCmdArgKeyRe.MatchString(k) {
				return validatorOptions{}, fmt.Sprintf("validator.post_cmd_args[%q]: key must match %s", k, postCmdArgKeyRe.String())
			}
			opts.PostCmdArgs = append(opts.PostCmdArgs, postCmdKV{Key: k, Value: val})
		}
	}

	return opts, ""
}

// buildValidatorMCPServer constructs an mcp_servers entry that runs
// `kitsoki mcp-validator --schema <abs-path> [--output <path>]
// [--post-cmd ... --post-cmd-arg k=v ... --max-retries N --state-file <path>]`.
//
// A relative schema path is resolved through resolvePromptPathCtx: the per-call
// prompt renderer (WithPromptRenderer, rooted at THIS dispatch's story dir) wins,
// falling back to KITSOKI_APP_DIR only when no renderer is in ctx (CLI one-shots /
// tests / legacy). This is load-bearing for concurrent driving sessions in one
// studio process: each session.new(harness:live) overwrites the process-global
// KITSOKI_APP_DIR, so resolving against the env alone would let one session's story
// dir bleed into another's agent dispatch (issues/bugs/2026-06-23T100426Z-studio-
// concurrent-sessions-agent-schema-bleed.md). The renderer is per-dispatch, so it
// isolates each session's schema base. When outputPath is non-empty the validator
// will write each successful submit's payload to that file (atomic, last-call wins)
// so the parent can recover the canonical JSON.
func buildValidatorMCPServer(ctx context.Context, schemaPath, outputPath string, opts validatorOptions) (map[string]any, error) {
	resolved, err := resolveValidatorSchemaPath(ctx, schemaPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(resolved); err != nil {
		return nil, fmt.Errorf("schema %q not found: %w", resolved, err)
	}

	bin := os.Getenv(kitsokiBinaryEnv)
	if bin == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("locate kitsoki binary: %w", err)
		}
		bin = exe
	}
	cliArgs := []any{"mcp-validator", "--schema", resolved}
	if outputPath != "" {
		cliArgs = append(cliArgs, "--output", outputPath)
	}
	if opts.PostCmd != "" {
		cliArgs = append(cliArgs, "--post-cmd", opts.PostCmd)
		for _, kv := range opts.PostCmdArgs {
			cliArgs = append(cliArgs, "--post-cmd-arg", kv.Key+"="+kv.Value)
		}
		if opts.PostCmdCwd != "" {
			cwd := resolvePromptPathCtx(ctx, opts.PostCmdCwd)
			cliArgs = append(cliArgs, "--post-cmd-cwd", cwd)
		}
	}
	if opts.MaxRetries > 0 {
		cliArgs = append(cliArgs, "--max-retries", fmt.Sprintf("%d", opts.MaxRetries))
	}
	if opts.StateFilePath != "" {
		cliArgs = append(cliArgs, "--state-file", opts.StateFilePath)
	}
	return map[string]any{
		"command": bin,
		"args":    cliArgs,
	}, nil
}

func resolveValidatorSchemaPath(ctx context.Context, schemaPath string) (string, error) {
	if resolved, ok, err := storyauthoring.ResolveSchemaPath(schemaPath); ok || err != nil {
		return resolved, err
	}
	return resolvePromptPathCtx(ctx, schemaPath), nil
}

// AgentAskWithMCPHandler is no longer a registered verb (Phase 9 unregistered
// host.agent.ask_with_mcp from the host dispatcher). It survives as an
// internal Go-callable entry point for chat-aware metamode in
// internal/metamode/adapter.go, which depends on the chat-store and validator
// loop machinery this function carries. New verb call sites must use one of
// the five public verbs (extract / decide / ask / task / converse). When the
// metamode call path eventually migrates onto host.agent.converse (or a
// dedicated chat-aware agent abstraction), this function and its supporting
// helpers go away.
//
// Required args:
//   - prompt_path | prompt (string): path to a prompt template file. If
//     relative, resolved against KITSOKI_APP_DIR (set by the loader) or cwd.
//
// Optional args:
//   - working_dir   (string): cwd for the claude subprocess.
//   - mcp_servers   (map):    server-name → { command: str, args: [str],
//     env: {k:v} }. Materialized into a temp
//     --mcp-config JSON file for the duration of
//     the call. Empty/missing → no --mcp-config.
//   - output_format (string): "text" (default) or "json". When "json", the
//     handler additionally parses stdout as JSON
//     and exposes it as `stdout_json` for binding.
//   - schema        (string): informational; passed through unchanged. The
//     MCP server is responsible for enforcement.
//   - args          (map):    explicit prompt-template variables.  The
//     prompt is rendered with `expr.Env{Args:
//     <this map>}`, so the prompt references its
//     variables as `{{ args.X }}` (or any nested
//     path like `{{ args.context.issue_block }}`,
//     `{{ args.artifacts.phase_3.fix_description }}`).
//     When omitted, falls back to passing the full
//     call-args map as the template scope (legacy
//     behaviour) so existing rooms keep working —
//     new rooms should use the explicit `args:`
//     block to keep handler-control keys
//     (prompt/schema/etc.) out of the template
//     namespace.
//   - chat_id       (string, optional): when set AND a ChatStore is in context,
//     persists the conversation to the chat transcript
//     and reuses the claude session ID stored on the
//     chat row across turns (same as host.agent.talk).
//   - system_prompt (string, optional): inline persona/system-prompt
//     instruction; threaded to `claude --append-system-prompt`. Wins over
//     any agent: value when both are set.
//   - agent         (string, optional): name of an entry in AppDef.Agents
//     (injected via WithAgents); supplies SystemPrompt + Model for this call.
//
// Returns Result.Data with:
//   - stdout      (string): claude's text reply
//   - stdout_json (any):    parsed JSON when output_format=="json" and parse succeeds
//   - exit_code   (int):    claude's exit code
//   - ok          (bool):   exit_code == 0
//   - chat_id            (string, chat-aware path only)
//   - claude_session_id  (string, chat-aware path only)
//   - transcript_seq     (int, chat-aware path only)
//   - answer             (string, chat-aware path only): alias for stdout, so
//     YAML can `bind: answer: answer`
//     consistently with host.agent.talk.
//
// On all expected errors (binary missing, prompt unreadable, MCP config
// marshal failure, non-zero exit) the handler returns Result{Error: ...}
// rather than a Go error so on_error: routing remains deterministic.
//
// # Per-call agent: arg (WS-A7)
//
// When the `agent:` arg is set, the handler looks up the named agent in
// the process-wide registry (see SetAgentRegistry) and uses the agent's
// SystemPrompt as the prompt body, the agent's Tools as the MCP tool
// allowlist hint, and the agent's DefaultCwd as the working directory
// when `working_dir:` is not also supplied. `agent:` and `prompt_path:`
// (or `prompt:`) are mutually exclusive — supplying both is an error.
// An unknown agent name is an error too; the handler does NOT silently
// fall back to prompt_path-style dispatch.
func AgentAskWithMCPHandler(ctx context.Context, args map[string]any) (Result, error) {
	agentName, _ := args["agent"].(string)
	agentName = strings.TrimSpace(agentName)

	promptPath, _ := args["prompt_path"].(string)
	promptPath = strings.TrimSpace(promptPath)
	if promptPath == "" {
		// Accept the proposal-style "prompt:" alias too.
		if alt, _ := args["prompt"].(string); strings.TrimSpace(alt) != "" {
			promptPath = alt
		}
	}

	// Mutual exclusion: agent: drives the entire prompt + tools + cwd
	// surface; prompt_path: is the legacy file-driven path. Mixing them
	// would be ambiguous about which prompt wins, so reject loudly.
	if agentName != "" && promptPath != "" {
		return Result{Error: "host.agent.ask_with_mcp: agent: and prompt_path: (or prompt:) are mutually exclusive — set only one"}, nil
	}

	if agentName != "" {
		return runAgentAskWithMCPViaAgent(ctx, agentName, args)
	}

	if promptPath == "" {
		return Result{Error: "host.agent.ask_with_mcp: prompt_path (or prompt) argument is required"}, nil
	}

	resolved := resolvePromptPathCtx(ctx, promptPath)
	raw, err := readPromptFile(resolved)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: read prompt %q: %v", resolved, err)}, nil
	}

	// Choose the template scope: prefer the explicit `args:` map, fall
	// back to the entire call-args dict for backwards compatibility.
	// Authors should migrate to the explicit form so handler-control
	// keys (prompt/schema/output_format/mcp_servers/working_dir) don't
	// leak into the prompt's `args.X` namespace and so nested objects
	// (`args.context.X`, `args.artifacts.phase_3.Y`) are clean rather
	// than a flat soup of one-deep keys.
	templateArgs, _ := args["args"].(map[string]any)
	if templateArgs == nil {
		templateArgs = args
	}
	rendered, err := renderPromptBytes(ctx, string(raw), templateArgs)
	if err != nil {
		return Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: render prompt %q: %v", resolved, err)}, nil
	}
	// Strip source-color sentinels from the rendered prompt before it
	// crosses the boundary into claude. Bound LLM values (e.g.
	// world.reproduction_artifact.summary_markdown) carry sentinels
	// for the display pipeline; claude doesn't need them and they
	// would otherwise consume tokens for no semantic value.
	rendered = sourcecolor.Strip(rendered)

	// Always-on editor context: append the operator's live `/ide` selection so
	// it feeds the request without the prompt having to reference args.ide. A
	// no-op when no selection rode the turn. Applied before the chat and core
	// dispatch paths so both carry it.
	rendered = appendIDEAmbient(ctx, rendered)
	// Always-on screen context: append the operator's pointed-at element/frame
	// beside the editor selection. A no-op when no surface attached a bundle.
	rendered = appendVisualAmbient(ctx, rendered)

	// Chat-aware path: chat_id provided AND ChatStore available.
	chatID, _ := args["chat_id"].(string)
	if chatID != "" {
		cs := ChatStoreFromContext(ctx)
		if cs == nil {
			return Result{Error: "host.agent.ask_with_mcp: chat_id provided but no chat store wired"}, nil
		}
		return runAgentAskWithMCPWithChat(ctx, cs, chatID, rendered, resolved, args)
	}

	// Non-chat path: callers that own their own conversation persistence
	// (e.g. internal/metamode) can pass claude_session_id to keep
	// Claude-side context across turns. If absent, we mint a UUID so the
	// caller can capture it from the result and pass it back next turn.
	claudeSID, _ := args["claude_session_id"].(string)
	minted := claudeSID == ""
	if minted {
		claudeSID = newUUID()
	}
	out, err := agentAskWithMCPCore(ctx, rendered, resolved, args, nil, claudeSID, minted)
	if err != nil {
		return out, err
	}
	if out.Data == nil {
		out.Data = make(map[string]any)
	}
	out.Data["claude_session_id"] = claudeSID
	return out, nil
}

// runAgentAskWithMCPViaAgent dispatches an ask_with_mcp call against a
// named agent. The agent's SystemPrompt becomes the prompt body (rendered
// through expr with any `args:` map the caller supplied), the agent's
// Tools become the tool-allowlist hint, and the agent's DefaultCwd is
// used as `working_dir:` when the caller did not set one explicitly.
//
// The function constructs a fresh args map with `agent:` stripped and
// `prompt_path:` set to a tempfile carrying the rendered system prompt,
// then re-enters AgentAskWithMCPHandler. The recursive call cannot
// re-trigger this branch because `agent:` is no longer set.
//
// Errors:
//   - no registry wired (SetAgentRegistry never called): clear error.
//   - unknown agent name: error includes the name and the list of
//     registered names so authoring mistakes surface immediately.
func runAgentAskWithMCPViaAgent(ctx context.Context, agentName string, args map[string]any) (Result, error) {
	reg := AgentRegistry()
	if reg == nil {
		return Result{Error: fmt.Sprintf(
			"host.agent.ask_with_mcp: agent: %q requested but no agent registry is wired (call host.SetAgentRegistry at startup)",
			agentName,
		)}, nil
	}
	ag, ok := reg.Get(agentName)
	if !ok {
		return Result{Error: fmt.Sprintf(
			"host.agent.ask_with_mcp: unknown agent %q (registered: %s)",
			agentName, strings.Join(reg.List(), ", "),
		)}, nil
	}

	// Render the agent's SystemPrompt with the caller's `args:` scope
	// (same template semantics the prompt_path path uses for its file
	// content). Falling back to the full args map for back-compat would
	// leak handler-control keys; keep it strict for the new code path.
	templateArgs, _ := args["args"].(map[string]any)
	if templateArgs == nil {
		templateArgs = map[string]any{}
	}
	rendered, rerr := renderPromptBytes(ctx, ag.SystemPrompt, templateArgs)
	if rerr != nil {
		return Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: render agent %q SystemPrompt: %v", agentName, rerr)}, nil
	}
	// Strip sentinels before writing the system prompt to disk for
	// claude — see render-prompt strip above.
	rendered = sourcecolor.Strip(rendered)

	promptPath, cleanup, perr := WritePromptTempFile(rendered)
	if perr != nil {
		return Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: %v", perr)}, nil
	}
	defer cleanup()

	// Build a fresh args map. We copy the caller's keys (so chat_id,
	// schema, validator, output_format, mcp_servers, etc. all flow
	// through) but drop agent: (so the recursive call doesn't re-enter
	// this branch) and prefer the agent's tool allowlist + cwd.
	next := make(map[string]any, len(args)+2)
	for k, v := range args {
		if k == "agent" {
			continue
		}
		next[k] = v
	}
	next["prompt_path"] = promptPath

	// working_dir precedence: caller-supplied beats agent default. Today
	// the handler reads `working_dir`; the agent's DefaultCwd is plumbed
	// into that slot so the existing handler path picks it up unchanged.
	if _, set := next["working_dir"].(string); !set || strings.TrimSpace(asString(next["working_dir"])) == "" {
		if ag.DefaultCwd != "" {
			next["working_dir"] = ag.DefaultCwd
		}
	}

	// Tool allowlist hint. The handler today does NOT gate by tool name
	// (tool-name gating is a future extension; see docs/stories/meta-mode.md); we plumb the list
	// through under the same key the metamode controller uses so a
	// future gating pass has one consistent place to look. Tests assert
	// on this key to confirm the agent's tool surface reached the call
	// site.
	if len(ag.Tools) > 0 {
		next["__meta_tool_allowlist"] = append([]string(nil), ag.Tools...)
	}

	return AgentAskWithMCPHandler(ctx, next)
}

// asString coerces an interface{} to its string form when the underlying
// value is a string; returns "" for any other type (including nil).
// Used by the agent path to decide whether `working_dir` is already set.
func asString(v any) string {
	s, _ := v.(string)
	return s
}

// runAgentAskWithMCPWithChat executes the chat-aware path: acquires the
// per-chat lock, persists the claude session ID, appends the user message,
// runs the claude invocation, then appends the assistant message.
//
// Step ordering: SetClaudeSessionID runs BEFORE the user-append so a write
// failure on the session ID can't strand an unanswered user message in a
// chat that has no claude session to resume (see I10 in the agent-rooms
// review). Likewise, an assistant-append failure surfaces via Result.Error
// so on_error: routing observes it.
func runAgentAskWithMCPWithChat(ctx context.Context, cs ChatStore, chatID, rendered, resolvedPrompt string, args map[string]any) (Result, error) {
	var out Result
	lockErr := cs.WithLock(ctx, chatID, func(ctx context.Context) error {
		chat, err := cs.Get(ctx, chatID)
		if err != nil {
			out = Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: get chat %s: %v", chatID, err)}
			return nil
		}

		// Determine or assign the claude session ID FIRST (before mutating
		// the transcript). If this write fails we bail before appending
		// anything.
		claudeSID := chat.ClaudeSessionID
		minted := claudeSID == ""
		if minted {
			claudeSID = newUUID()
			if err := cs.SetClaudeSessionID(ctx, chatID, claudeSID); err != nil {
				out = Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: set claude session id: %v", err)}
				return nil
			}
		}

		// Append the rendered prompt as the user message.
		if _, err := cs.AppendMessage(ctx, chatID, "user", rendered, nil); err != nil {
			out = Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: append user message: %v", err)}
			return nil
		}

		inner, runErr := agentAskWithMCPCore(ctx, rendered, resolvedPrompt, args, nil, claudeSID, minted)
		if runErr != nil {
			return runErr
		}

		// On success, append the assistant message.  Surface persistence
		// failures via Result.Error so on_error: routing fires (the user
		// has the answer in inner.Data["stdout"], but the orchestrator/TUI
		// is informed the transcript wasn't extended).
		if inner.Error == "" {
			stdout, _ := inner.Data["stdout"].(string)
			exitCode, _ := inner.Data["exit_code"].(int)
			_, hasSubmitted := inner.Data["submitted"]
			_, appendErr := cs.AppendMessage(ctx, chatID, "assistant", stdout, map[string]any{
				"exit_code":           exitCode,
				"validator_submitted": hasSubmitted,
			})
			if appendErr != nil {
				if inner.Data == nil {
					inner.Data = make(map[string]any)
				}
				inner.Data["chat_id"] = chatID
				inner.Data["claude_session_id"] = claudeSID
				inner.Error = fmt.Sprintf("host.agent.ask_with_mcp: persist assistant message: %v", appendErr)
				out = inner
				return nil
			}
		}

		seq, _ := cs.LatestSeq(ctx, chatID)

		// Enrich the result with chat metadata.
		if inner.Data == nil {
			inner.Data = make(map[string]any)
		}
		inner.Data["chat_id"] = chatID
		inner.Data["claude_session_id"] = claudeSID
		inner.Data["transcript_seq"] = seq
		// N11: expose `answer` as an alias for `stdout` in the chat-aware
		// path so YAML can write `bind: answer: answer` regardless of which
		// chat-aware handler produced the result. `stdout` remains the
		// canonical name for the non-chat path; we only add this in the
		// chat-aware branch where a transcript-anchored "answer" is more
		// meaningful than a raw stdout dump.
		if stdout, ok := inner.Data["stdout"].(string); ok {
			inner.Data["answer"] = stdout
		}

		out = inner
		return nil
	})
	if errors.Is(lockErr, ErrChatBusy) {
		return Result{Error: lockErr.Error()}, nil
	}
	if lockErr != nil {
		return Result{}, lockErr
	}
	return out, nil
}

// agentAskWithMCPCore executes the claude one-shot invocation. When
// claudeSessionID is non-empty:
//   - claudeSessionMinted == true  → --session-id (claude creates this id)
//   - claudeSessionMinted == false → --resume     (claude looks up an existing one)
//
// Mixing the two yields claude's "Session ID … is already in use" error.
func agentAskWithMCPCore(ctx context.Context, rendered, resolvedPrompt string, args map[string]any, _ any, claudeSessionID string, claudeSessionMinted bool) (Result, error) {
	bin, err := resolveAgentBin(ctx)
	if err != nil {
		return Result{Error: err.Error()}, nil
	}

	workingDir, _ := args["working_dir"].(string)
	if workingDir == "" {
		workingDir = filepath.Dir(resolvedPrompt)
	}

	outputFormat := "text"
	if of, _ := args["output_format"].(string); of != "" {
		outputFormat = of
	} else if StreamSinkFrom(ctx) != nil {
		// Auto-enable streaming when a sink is wired into the context.
		// The TUI installs one for every turn so on_enter agent calls
		// stream live progress events into the chat transcript — same
		// surface meta-mode already uses. Callers that explicitly want
		// buffered text output can still set output_format: text.
		outputFormat = "stream-json"
	}

	cliArgs := []string{
		"-p",
		"--output-format", outputFormat,
		"--permission-mode", "bypassPermissions",
	}
	// claude requires --verbose alongside --output-format stream-json
	// in -p (one-shot) mode; without it the binary exits with a usage
	// error. Only metamode opts into stream-json today (see
	// internal/metamode/adapter.go); all other callers default to
	// "text" and skip this branch.
	if outputFormat == "stream-json" {
		cliArgs = append(cliArgs, "--verbose")
	}

	// When participating in a chat, inject the session ID so Claude can
	// resume the conversation from its own memory. --session-id is for
	// the FIRST invocation with this id (claude assigns/creates it);
	// --resume is for subsequent invocations (claude looks it up).
	if claudeSessionID != "" {
		if claudeSessionMinted {
			cliArgs = append(cliArgs, "--session-id", claudeSessionID)
		} else {
			cliArgs = append(cliArgs, "--resume", claudeSessionID)
		}
	}

	// Per-call agent + inline system_prompt — same shape as the other two
	// agent handlers (host.agent.{ask,talk}). The retry-loop path runs
	// claude --resume on subsequent iterations, but resume carries the
	// original session's system prompt forward implicitly, so we only
	// need to append the flag on iteration 0 (handled by BaseCLIArgs).
	agent, _ := resolveAgent(ctx, args)
	ctx, agent = applyProvider(ctx, args, agent)
	cliArgs, _ = appendComposedSystemPrompt(ctx, cliArgs, sysprompt.AskWithMCP,
		effectiveSystemPrompt(args, agent), agent.InheritClaudeDefault)
	if strings.TrimSpace(agent.Model) != "" {
		cliArgs = append(cliArgs, "--model", agent.Model)
	}
	// Thread agent tools (or per-call override) via --allowedTools. Per-call
	// wins over agent.Tools per D5; effectiveTools emits a warn-line on conflict.
	tools := effectiveTools(ctx, args, agent)
	if len(tools) > 0 {
		cliArgs = appendAllowedToolsFlag(cliArgs, tools)
	}
	// Hard-deny AskUserQuestion: headless `-p` auto-resolves it with empty
	// answers (see alwaysDeniedTools).
	cliArgs = appendDisallowedToolsFlag(cliArgs, alwaysDeniedTools)

	// Build the merged mcp_servers map: caller-provided entries plus an
	// auto-attached "validator" entry when `schema:` is set and the caller
	// didn't already define one. The validator is the running kitsoki binary
	// invoked as `kitsoki mcp-validator --schema <abs-path>`, which exposes a
	// `submit` tool whose input schema is the user-provided schema. The LLM
	// must call submit() and round-trip its JSON through it before answering;
	// validation errors come back inline so the LLM self-corrects.
	//
	// We shallow-copy the caller's map so the auto-attached validator entry
	// does not leak back into args["mcp_servers"] (an effect map can be
	// re-run after on_error: routing, and a stale validator entry would
	// point at a deleted tempfile on the second pass).
	callerServers, _ := args["mcp_servers"].(map[string]any)
	mcpServers := make(map[string]any, len(callerServers)+1)
	for k, v := range callerServers {
		mcpServers[k] = v
	}

	// Parse the optional `validator:` sub-block. When absent the
	// resulting validatorOptions is the zero value and the validator
	// runs schema-only (the v0 behaviour). Errors here are user-visible
	// (malformed YAML); surface as Result.Error so on_error: routes.
	vopts, vparseErr := parseValidatorOptions(args)
	if vparseErr != "" {
		return Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: %s", vparseErr)}, nil
	}
	// validatorBlockPresent governs whether we run the abandonment-recovery
	// retry loop (claude --resume nudges) or the single-shot legacy path.
	// We key on the explicit presence of the YAML sub-block rather than on
	// any individual field so authors who write `validator: {}` opt into
	// the retry semantics with sensible defaults.
	_, validatorBlockPresent := args["validator"]

	// validatorOutputPath is set when we auto-attach the validator. It's
	// the path the validator subprocess writes the schema-validated payload
	// to on each successful submit. We read it back after `claude -p` exits
	// and bind the result as Result.Data["submitted"], so authors can
	// `bind: foo: submitted` and get the canonical, validated JSON instead
	// of relying on the LLM's final stdout text.
	//
	// validatorStateFilePath is set when the retry loop is active so the
	// validator's session counters (attempts/successful_submits/last_error)
	// survive each iteration's subprocess restart. We read this same file
	// between iterations to inspect the Outcome and decide whether to
	// re-engage with a nudge.
	var validatorOutputPath string
	var validatorStateFilePath string
	if schemaArg, _ := args["schema"].(string); strings.TrimSpace(schemaArg) != "" {
		if _, alreadyHasValidator := mcpServers["validator"]; !alreadyHasValidator {
			outFile, ofErr := os.CreateTemp("", "kitsoki-validated-*.json")
			if ofErr != nil {
				return Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: create validator output tempfile: %v", ofErr)}, nil
			}
			validatorOutputPath = outFile.Name()
			_ = outFile.Close()
			// Remove the empty tempfile so we can detect "validator never
			// captured anything" by os.Stat returning ErrNotExist after
			// claude exits — the validator will recreate it via atomic
			// rename on its first successful submit.
			_ = os.Remove(validatorOutputPath)
			defer os.Remove(validatorOutputPath)

			// In retry-loop mode, allocate a state-file path and pass it
			// to the validator subprocess so counters persist across
			// `claude --resume` restarts. Outside retry-loop mode we
			// don't bother — the legacy single-shot path doesn't need
			// cross-process state.
			if validatorBlockPresent {
				stFile, sfErr := os.CreateTemp("", "kitsoki-validator-state-*.json")
				if sfErr != nil {
					return Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: create validator state tempfile: %v", sfErr)}, nil
				}
				validatorStateFilePath = stFile.Name()
				_ = stFile.Close()
				_ = os.Remove(validatorStateFilePath)
				defer os.Remove(validatorStateFilePath)
				vopts.StateFilePath = validatorStateFilePath
			}

			validatorEntry, vErr := buildValidatorMCPServer(ctx, schemaArg, validatorOutputPath, vopts)
			if vErr != nil {
				return Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: %v", vErr)}, nil
			}
			mcpServers["validator"] = validatorEntry
		}
	}

	mcpServers = attachStudioMCPServer(mcpServers, tools)

	// Materialize mcp_servers (if any) into a temp config file.
	if len(mcpServers) > 0 {
		mcpConfigPath, cleanup, cfgErr := writeMCPConfigTempfile(mcpServers, "kitsoki-mcp")
		if cfgErr != nil {
			return Result{Error: fmt.Sprintf("host.agent.ask_with_mcp: %v", cfgErr)}, nil
		}
		defer cleanup()
		cliArgs = append(cliArgs, "--mcp-config", mcpConfigPath)
	}

	// Pick the execution path:
	//   - validator: block present  → abandonment-recovery retry loop
	//                                  with `claude --resume <sid>` nudges
	//   - otherwise                  → single-shot legacy path (unchanged)
	if validatorBlockPresent {
		effectiveMaxRetries := vopts.MaxRetries
		if effectiveMaxRetries <= 0 {
			effectiveMaxRetries = validatorDefaultMaxRetries
		}
		return runWithValidatorRetryLoop(ctx, runValidatorLoopParams{
			Bin:                 bin,
			BaseCLIArgs:         cliArgs,
			Rendered:            rendered,
			WorkingDir:          workingDir,
			OutputFormat:        outputFormat,
			ValidatorOutputPath: validatorOutputPath,
			ValidatorStatePath:  validatorStateFilePath,
			MaxOuterIterations:  maxOuterIterations,
			ValidatorMaxRetries: effectiveMaxRetries,
		}), nil
	}

	var (
		cr           ClaudeRun
		runErr       error
		streamSID    string
		usedStreamer bool
	)
	if outputFormat == "stream-json" {
		usedStreamer = true
		cr, streamSID, runErr = runClaudeStreamJSON(ctx, bin, cliArgs, rendered, workingDir)
	} else {
		cr, runErr = runClaudeOneShot(ctx, bin, cliArgs, rendered, workingDir)
	}
	if runErr != nil {
		return Result{}, runErr
	}
	if cr.Infra != nil {
		msg := fmt.Sprintf("host.agent.ask_with_mcp: claude exec failed: %v", cr.Infra)
		if s := strings.TrimSpace(cr.Stderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		return Result{Error: msg}, nil
	}

	res := Result{
		Data: map[string]any{
			// Wrap stdout at the operator boundary — see
			// internal/render/sourcecolor. The wrap is zero-width and
			// survives pongo render, hardwrap, and JSON serialization
			// (including the json output_format unmarshal below, which
			// runs on the unwrapped cr.Stdout directly).
			"stdout":    sourcecolor.Wrap(cr.Stdout),
			"exit_code": cr.ExitCode,
			"ok":        cr.ExitCode == 0,
		},
	}
	// When the streamer extracted a session_id from the stream's
	// system.init / result event, prefer it over the host-minted UUID
	// so downstream resume calls hit the same Claude-side session.
	// (Defensive: claude normally honors --session-id we passed in, so
	// streamSID typically equals claudeSessionID; if they diverge, the
	// stream's value is canonical.)
	if usedStreamer && streamSID != "" {
		res.Data["claude_session_id"] = streamSID
	}

	if outputFormat == "json" && cr.ExitCode == 0 && cr.Stdout != "" {
		var parsed any
		if jErr := json.Unmarshal([]byte(cr.Stdout), &parsed); jErr == nil {
			// Do NOT wrap string leaves with source-color sentinels:
			// stdout_json values feed `set:` bindings and `when:`
			// guards in the state machine, and they're forwarded
			// verbatim to external transports (Jira/Bitbucket
			// comments) by host.transport.post and the bugfix
			// pr-reply/pr-apply steps.  Wrapping here injects
			// zero-width Unicode markers that break enum-equality
			// guards (`action == 'edit'`) and leak into Jira/BB
			// audit trails.  Source-color belongs at the TUI render
			// boundary; the renderer knows the field provenance via
			// its own metadata and re-applies markers at paint time.
			res.Data["stdout_json"] = parsed
		} else {
			// Don't fail the handler — bind: { foo: stdout_json } will silently
			// not bind, and an explicit on_error: route can still fire if the
			// state machine treats absent-binding as a failure. The text stdout
			// remains available for diagnostics.
			res.Data["stdout_json_parse_error"] = jErr.Error()
		}
	}

	// If we auto-attached the validator, read back the canonical payload it
	// captured from the LLM's submit() call. This is the schema-validated
	// JSON, independent of whatever final prose claude wrote — bind to
	// `submitted` for the typed-output path.
	if validatorOutputPath != "" {
		vBytes, vErr := kitsokimcp.ReadCapturedPayload(validatorOutputPath)
		if vErr == nil && len(vBytes) > 0 {
			var parsed any
			if jErr := json.Unmarshal(vBytes, &parsed); jErr == nil {
				// Do NOT wrap string leaves with source-color
				// sentinels here.  `submitted` is the MCP-validated,
				// schema-conforming canonical input for the state
				// machine — bug-fix's phase_12_6 reads
				// `world.phase_12_6_submitted.action == 'edit'`,
				// and the bugfix pr-reply / pr-apply steps copy
				// `submitted.comment_replies[].reply_text` verbatim
				// into Bitbucket PR comments and Jira posts.
				// Wrapping injects zero-width Unicode markers that
				// (a) break enum-equality guards in app.yaml and
				// (b) leak into Jira/BB audit trails.  Source-color
				// markers belong at the TUI render boundary; the
				// renderer applies them at paint time from field
				// provenance metadata, not by mutating world data
				// at write time.
				res.Data["submitted"] = unescapeOverEscapedStrings(parsed)
			} else {
				// The validator only writes payloads that already passed
				// schema validation, so a parse error here is a real bug.
				// Surface it through the handler error path so on_error:
				// can route.
				res.Error = fmt.Sprintf("host.agent.ask_with_mcp: parse validator output: %v", jErr)
			}
		}
		// Note: file-not-found is the normal "LLM never made a successful
		// submit" case. We leave Result.Data["submitted"] unset; the
		// state machine handles its absence via guard / on_error.
	}

	// Preserve any earlier res.Error (e.g. validator parse-error) — only
	// fall back to the exit-code message when nothing more specific is set.
	if cr.ExitCode != 0 && res.Error == "" {
		res.Error = claudeExitErrorMessage(cr.ExitCode, cr.Stderr, cr.Stdout)
	}
	return res, nil
}

// maxOuterIterations is the default cap on the outer claude-restart loop
// when a validator: block is present. The inner per-submit retries are
// capped separately by the validator's MaxRetries field. Combined cap is
// outer * inner = 3 * 5 = 15 submits in the worst case.
const maxOuterIterations = 3

// abandonmentNudgePrompt is the prompt sent on each `claude --resume`
// re-engagement after the LLM exited without a successful submit. It is
// rendered with the validator's `last_error` (if any) so the LLM sees the
// most recent rejection reason and corrects accordingly. Hard-coded for
// now; can be lifted into a YAML field on a future iteration if authors
// need per-room customisation.
const abandonmentNudgeTemplate = `Your previous turn ended without successfully calling submit.{{LAST_ERROR_BLOCK}}

You MUST call the validator's submit tool with a valid payload to continue.
Read the task again, identify what is needed, and call submit. Do not exit
the conversation without submitting.`

// runValidatorLoopParams bundles everything the retry loop needs. Pulling
// it into a struct keeps AgentAskWithMCPHandler's signature clean even as
// the loop grows new knobs (custom nudge, per-iteration logging, etc.).
type runValidatorLoopParams struct {
	Bin                 string
	BaseCLIArgs         []string // contains -p, --output-format, --permission-mode, --mcp-config <path>
	Rendered            string   // initial user prompt
	WorkingDir          string
	OutputFormat        string
	ValidatorOutputPath string
	ValidatorStatePath  string
	MaxOuterIterations  int
	// ValidatorMaxRetries mirrors validatorOptions.MaxRetries (or the
	// validator's default of 5 when unset). The host-side outcome
	// computation must agree with the in-validator one or we'd
	// misclassify "exhausted" as "abandoned" and waste an extra
	// re-engagement.
	ValidatorMaxRetries int
}

// runWithValidatorRetryLoop runs `claude -p` once per outer iteration,
// re-engaging via `claude --resume <session-id>` whenever the LLM exits
// without a successful submit. Between iterations it reads the validator
// state file to inspect Outcome and last_error.
//
// Outer-loop semantics:
//
//	iteration 0  : claude -p   --session-id <sid>
//	iteration N>0: claude      --resume     <sid>  (only when Outcome == Abandoned)
//
// Termination conditions (checked after each iteration):
//
//	Outcome == Success            → return success, bind submitted payload
//	Outcome == RetriesExhausted   → return error (last_error), on_error: fires
//	Outcome == Abandoned, n+1==N  → return error ("session abandoned"), on_error: fires
//	Outcome == Abandoned, n+1<N   → continue with --resume + nudge prompt
//
// The validator's in-memory counters reset between subprocess invocations,
// but the state-file path makes them persist so the validator can return
// the "MAX RETRIES EXHAUSTED" sentinel even if exhaustion straddles
// re-engagements (rare but possible — the LLM can submit many times within
// a single iteration, so iteration 1's submits accrue against iteration
// 0's attempts counter).
func runWithValidatorRetryLoop(ctx context.Context, p runValidatorLoopParams) Result {
	maxOuter := p.MaxOuterIterations
	if maxOuter <= 0 {
		maxOuter = maxOuterIterations
	}

	sessionID := newUUID()

	var lastIterStdout string
	var lastIterExit int
	var lastIterStderr string
	var lastInfraErr error
	for iter := 0; iter < maxOuter; iter++ {
		// Build the per-iteration argv. iter==0 starts a fresh session;
		// iter>0 resumes the same session so claude has the full prior
		// context (prompt, tool calls, validator rejections) when the
		// nudge arrives.
		var iterArgs []string
		var iterPrompt string
		if iter == 0 {
			iterArgs = append([]string{}, p.BaseCLIArgs...)
			iterArgs = append(iterArgs, "--session-id", sessionID)
			iterPrompt = p.Rendered
		} else {
			iterArgs = append([]string{}, p.BaseCLIArgs...)
			iterArgs = append(iterArgs, "--resume", sessionID)
			// Inspect the validator's recorded last_error so the nudge
			// can echo the most recent rejection reason.
			_, _, lastErr := kitsokimcp.ReadStateFile(p.ValidatorStatePath)
			iterPrompt = renderNudge(lastErr)
		}

		cr, runErr := runClaudeOneShot(ctx, p.Bin, iterArgs, iterPrompt, p.WorkingDir)
		if runErr != nil {
			lastInfraErr = runErr
			break
		}
		if cr.Infra != nil {
			lastInfraErr = cr.Infra
			lastIterStderr = cr.Stderr
			break
		}
		lastIterStdout = cr.Stdout
		lastIterExit = cr.ExitCode
		lastIterStderr = cr.Stderr

		// Inspect the validator's outcome. If it has a successful submit
		// we're done. If it ran out of retries we're done (failure). If
		// the LLM abandoned without submitting, loop and re-engage —
		// unless we've used the outer budget.
		attempts, success, lastErr := kitsokimcp.ReadStateFile(p.ValidatorStatePath)
		switch outcomeFromState(attempts, success, p.ValidatorMaxRetries) {
		case mcpOutcomeSuccess:
			return assembleResult(p, lastIterStdout, lastIterExit, lastIterStderr, "")
		case mcpOutcomeRetriesExhausted:
			msg := lastErr
			if strings.TrimSpace(msg) == "" {
				msg = fmt.Sprintf("validator: max retries exhausted after %d attempts", attempts)
			}
			return assembleResult(p, lastIterStdout, lastIterExit, lastIterStderr, msg)
		case mcpOutcomeAbandoned:
			// Try again unless we've spent the outer budget.
			continue
		}
	}

	// Outer budget spent (or infrastructure failure). Surface whatever we
	// know: prefer the validator's last_error, fall back to "abandoned",
	// fall back to infra error.
	if lastInfraErr != nil {
		msg := fmt.Sprintf("host.agent.ask_with_mcp: claude exec failed: %v", lastInfraErr)
		if s := strings.TrimSpace(lastIterStderr); s != "" {
			msg = fmt.Sprintf("%s\nstderr: %s", msg, s)
		}
		return Result{Error: msg}
	}
	attempts, _, lastErr := kitsokimcp.ReadStateFile(p.ValidatorStatePath)
	msg := lastErr
	if strings.TrimSpace(msg) == "" {
		msg = fmt.Sprintf("validator: session abandoned without successful submit after %d outer iteration(s), %d attempt(s)", maxOuter, attempts)
	}
	return assembleResult(p, lastIterStdout, lastIterExit, lastIterStderr, msg)
}

// assembleResult builds the standard handler return value from a final
// claude run. Mirrors the legacy single-shot path so authors see the
// same Data shape (stdout / exit_code / ok / submitted / stdout_json)
// regardless of which path executed.
//
// errMsg is the orchestrator-visible error: empty = success, non-empty
// goes to res.Error which the orchestrator copies into world.last_error
// and (when on_error: is declared on the host call) routes the journey.
func assembleResult(p runValidatorLoopParams, stdout string, exitCode int, stderr, errMsg string) Result {
	res := Result{
		Data: map[string]any{
			// Wrap stdout at the operator boundary — see
			// internal/render/sourcecolor. The json unmarshal below
			// uses the unwrapped stdout var directly.
			"stdout":    sourcecolor.Wrap(stdout),
			"exit_code": exitCode,
			"ok":        exitCode == 0,
		},
	}
	if p.OutputFormat == "json" && exitCode == 0 && stdout != "" {
		var parsed any
		if jErr := json.Unmarshal([]byte(stdout), &parsed); jErr == nil {
			// Do NOT wrap string leaves with source-color sentinels —
			// see the non-validator branch above: stdout_json feeds
			// `set:` bindings and `when:` guards and is forwarded
			// verbatim to external transports (Jira/Bitbucket). Wrapping
			// injects zero-width markers that break enum-equality guards
			// (`action == 'edit'`) and leak into audit trails.
			res.Data["stdout_json"] = parsed
		} else {
			res.Data["stdout_json_parse_error"] = jErr.Error()
		}
	}
	if p.ValidatorOutputPath != "" {
		vBytes, vErr := kitsokimcp.ReadCapturedPayload(p.ValidatorOutputPath)
		if vErr == nil && len(vBytes) > 0 {
			var parsed any
			if jErr := json.Unmarshal(vBytes, &parsed); jErr == nil {
				// Do NOT wrap string leaves with source-color sentinels —
				// `submitted` is the MCP-validated canonical input for the
				// state machine (bugfix reads `submitted.action == 'edit'`
				// and copies `submitted.comment_replies[].reply_text`
				// verbatim into Bitbucket/Jira). Wrapping injects zero-width
				// markers that break enum-equality guards and leak into
				// audit trails. This matches the non-validator path above.
				res.Data["submitted"] = unescapeOverEscapedStrings(parsed)
			} else {
				// The validator only writes schema-passed payloads; a
				// parse error here is a real bug.
				if errMsg == "" {
					errMsg = fmt.Sprintf("host.agent.ask_with_mcp: parse validator output: %v", jErr)
				}
			}
		}
	}
	if errMsg != "" {
		res.Error = errMsg
	} else if exitCode != 0 {
		res.Error = claudeExitErrorMessage(exitCode, stderr, stdout)
	}
	return res
}

// renderNudge interpolates the LLM-facing nudge prompt with the most
// recent rejection reason (if any). Hard-coded template for now; if/when
// a room needs a different tone the call site can plumb a string field
// through validatorOptions.
func renderNudge(lastError string) string {
	block := ""
	if strings.TrimSpace(lastError) != "" {
		block = "\n\nThe last submission attempt was rejected:\n" + lastError
	}
	return strings.Replace(abandonmentNudgeTemplate, "{{LAST_ERROR_BLOCK}}", block, 1)
}

// outcomeFromState mirrors mcp.ValidatorServer.Outcome() at the host
// level — we reimplement here so the host package doesn't have to import
// the mcp package. The two implementations must stay in sync; the
// single-source-of-truth lives in mcp/validator.go.
type mcpOutcome int

const (
	mcpOutcomeUnknown mcpOutcome = iota
	mcpOutcomeSuccess
	mcpOutcomeRetriesExhausted
	mcpOutcomeAbandoned
)

func outcomeFromState(attempts, success, maxRetries int) mcpOutcome {
	if success >= 1 {
		return mcpOutcomeSuccess
	}
	if maxRetries > 0 && attempts >= maxRetries {
		return mcpOutcomeRetriesExhausted
	}
	return mcpOutcomeAbandoned
}

// validatorDefaultMaxRetries mirrors mcp.ValidatorConfig.MaxRetries's
// fallback. The host-side outcome computation must agree with the
// in-validator one or we'd misclassify exhaustion vs abandonment.
const validatorDefaultMaxRetries = 5

// unescapeOverEscapedStrings walks the parsed validator payload and
// fixes string values that arrive with literal "\n" / "\t" / "\r"
// instead of real newlines / tabs. Claude occasionally double-escapes
// when submitting structured JSON via the schema-validator MCP tool —
// claude writes `"summary_markdown": "## Title\\n\\n### Body"` where
// the inner `\\n` is JSON-source for the 2-char literal `\n`, not the
// 1-char newline escape. We see this only intermittently (turn 3 of
// the 2026-05-20 dogfood trace rendered cleanly; turn 4 of the same
// trace landed with literal `\n` everywhere), so this is best-treated
// as a defense at the seam where claude's output enters the world.
//
// The unescape is conservative: only strings that contain at least
// one of the over-escape signatures are rewritten, and only the three
// most-common escape pairs (\n, \t, \r) are converted. Strings that
// happen to mention `\n` legitimately (a string literal in
// documentation, say) AND have ≤ 3 such occurrences are left
// alone — most markdown bodies have far more line-breaks than that.
func unescapeOverEscapedStrings(v any) any {
	switch t := v.(type) {
	case string:
		return maybeUnescapeString(t)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = unescapeOverEscapedStrings(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = unescapeOverEscapedStrings(vv)
		}
		return out
	}
	return v
}

// maybeUnescapeString turns literal `\n` / `\t` / `\r` 2-char
// sequences into the matching control character — but only when the
// input has enough of them to suggest over-escaping rather than
// incidental mention. The threshold (≥ 3 of any one escape) tracks
// the dogfood symptom: a fix-proposal markdown body had 30+ literal
// `\n` separators; a typical docstring mentioning escape sequences
// has at most one or two.
func maybeUnescapeString(s string) string {
	if !strings.Contains(s, `\n`) && !strings.Contains(s, `\t`) && !strings.Contains(s, `\r`) {
		return s
	}
	if strings.Count(s, `\n`) < 3 && strings.Count(s, `\t`) < 3 && strings.Count(s, `\r`) < 3 {
		return s
	}
	r := strings.NewReplacer(
		`\n`, "\n",
		`\t`, "\t",
		`\r`, "\r",
	)
	return r.Replace(s)
}
