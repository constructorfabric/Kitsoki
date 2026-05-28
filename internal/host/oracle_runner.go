// Package host — internal helpers shared between the one-shot oracle
// handlers (host.oracle.ask, host.oracle.ask_with_mcp). Both run the
// claude CLI once with a rendered prompt piped on stdin and normalize
// exit / context errors the same way; this file is the seam.
package host

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// ClaudeRun is the outcome of one `claude -p` invocation.
type ClaudeRun struct {
	// Stdout has its trailing newline trimmed.
	Stdout string
	// Stderr is left raw (untrimmed) so callers can format messages.
	Stderr   string
	ExitCode int
	// Infra is non-nil iff the process failed for a reason other than
	// producing an exit code (e.g. binary vanished mid-run). Callers
	// surface this through Result.Error rather than a Go error so
	// on_error: routing stays deterministic.
	Infra error
	// RawEvents holds every parsed JSONL event from a stream-json run.
	// Populated by runClaudeStreamJSON; nil on the buffered-text path.
	// Callers (e.g. observeTaskToolCalls) read RawEvents instead of
	// re-parsing Stdout so they see the full stream including tool_use
	// and tool_result blocks that runClaudeStreamJSON discards from Stdout.
	RawEvents []json.RawMessage
}

// ClaudeRunner runs the claude binary one-shot and returns its
// outcome. Production wires the real exec via runClaudeOneShotReal;
// tests inject an in-process stub via WithClaudeRunner so the
// package's ~180 tests can run without forking 180 bash subprocesses.
type ClaudeRunner func(ctx context.Context, args []string, stdin, workingDir string) (ClaudeRun, error)

type claudeRunnerCtxKey struct{}

// WithClaudeRunner returns a child context that uses r for every
// runClaudeOneShot invocation reached through it. Tests use this to
// install in-process stubs. Production never calls WithClaudeRunner.
func WithClaudeRunner(ctx context.Context, r ClaudeRunner) context.Context {
	return context.WithValue(ctx, claudeRunnerCtxKey{}, r)
}

// ClaudeRunnerFromContext returns the runner installed in ctx, or
// nil if none is installed (the real-exec path is used).
func ClaudeRunnerFromContext(ctx context.Context) ClaudeRunner {
	r, _ := ctx.Value(claudeRunnerCtxKey{}).(ClaudeRunner)
	return r
}

// runClaudeOneShot dispatches a claude one-shot call. When a
// ClaudeRunner is installed in ctx (test setup) the stub is invoked
// in-process. Otherwise the real exec path runClaudeOneShotReal forks
// the binary as before.
//
// The trailing-newline trim on Stdout is enforced here, not in the
// runner implementations. The ClaudeRun.Stdout field is documented to
// have its trailing newline trimmed; the real exec path (and any
// stub) might naively return raw output. Enforce the contract at the
// dispatcher so callers (and downstream code that splits on \n) never
// see drift between exec and stubbed paths.
//
// The optional sessionID, when non-empty, is injected as KITSOKI_SESSION_ID
// into the subprocess environment per-subprocess (not via os.Setenv) so
// concurrent callers don't race on the global env. Callers that don't yet
// thread sessionID may omit the argument; existing call sites are unaffected.
func runClaudeOneShot(ctx context.Context, bin string, cliArgs []string, stdin, workingDir string, sessionID ...string) (ClaudeRun, error) {
	sid := ""
	if len(sessionID) > 0 {
		sid = sessionID[0]
	}
	var (
		cr  ClaudeRun
		err error
	)
	if r := ClaudeRunnerFromContext(ctx); r != nil {
		cr, err = r(ctx, cliArgs, stdin, workingDir)
	} else {
		cr, err = runClaudeOneShotReal(ctx, bin, cliArgs, stdin, workingDir, sid)
	}
	cr.Stdout = strings.TrimRight(cr.Stdout, "\n")
	return cr, err
}

// runClaudeOneShotReal executes the claude CLI with the given args, prompt
// piped on stdin, and working directory. Context cancellation is
// returned as a Go error; all other failures populate the ClaudeRun
// fields.
//
// Env: the kitsoki binary's own directory is prepended to PATH so
// agents whose tool surface includes Bash patterns that invoke
// `kitsoki <subcommand>` (e.g. `Bash(kitsoki bug create*)` on the
// bug-reporter agent) actually find the binary. Without this,
// `kitsoki` is only on PATH when the user has run `go install` or
// otherwise placed it there — which `go run ./cmd/kitsoki ...`
// users don't do, and the subprocess fails with "command not found".
//
// sessionID, when non-empty, is injected as KITSOKI_SESSION_ID into
// the subprocess environment without mutating the process-global env.
func runClaudeOneShotReal(ctx context.Context, bin string, cliArgs []string, stdin, workingDir, sessionID string) (ClaudeRun, error) {
	cmd := exec.CommandContext(ctx, bin, cliArgs...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Dir = workingDir
	cmd.Env = envWithSessionID(envWithKitsokiBinOnPath(os.Environ()), sessionID)

	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se

	runErr := cmd.Run()
	out := strings.TrimRight(so.String(), "\n")
	if runErr != nil {
		if ctx.Err() != nil {
			return ClaudeRun{}, ctx.Err()
		}
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return ClaudeRun{Stdout: out, Stderr: se.String(), ExitCode: exitErr.ExitCode()}, nil
		}
		return ClaudeRun{Stdout: out, Stderr: se.String(), Infra: runErr}, nil
	}
	return ClaudeRun{Stdout: out, Stderr: se.String()}, nil
}

// runClaudeStreamJSON is the streaming sibling of runClaudeOneShot.
// It runs `claude -p --output-format stream-json --verbose` and reads
// stdout line by line, emitting a "metamode.oracle.event" slog record
// for each JSONL event (system/assistant/user/result/etc.) so the trace
// surface shows live progress in real time. It then synthesizes a
// ClaudeRun whose Stdout is the assistant's final text reply (the
// `result` event's `result` field, or — as a fallback — the
// concatenation of any text-content blocks across all assistant
// events). Tests that install a ClaudeRunner stub via WithClaudeRunner
// see the stub run and the raw stub output get parsed line-by-line
// (no events when the stub emits plain text — the function falls back
// to using the entire stub stdout as the reply, preserving the legacy
// text-mode contract). The streaming branch only kicks in when no stub
// is wired AND the binary actually emits JSONL.
//
// sessionID, when non-empty, is injected as KITSOKI_SESSION_ID into
// the subprocess environment without mutating the process-global env.
//
// The returned ClaudeRun.RawEvents contains every parsed JSONL event
// (in order). Callers such as observeTaskToolCalls read RawEvents
// rather than re-parsing Stdout so they see the full stream including
// tool_use / tool_result blocks.
//
// Stream-event shapes (Anthropic-style) we recognise:
//   - {"type":"system","subtype":"init","session_id":"…",…}
//   - {"type":"assistant","message":{"content":[{"type":"text","text":"…"}]…}}
//   - {"type":"assistant","message":{"content":[{"type":"tool_use","name":"…","input":{…}}]}}
//   - {"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"…",…}]}}
//   - {"type":"result","subtype":"success","result":"<final text>",
//     "session_id":"…","total_cost_usd":…,"is_error":false}
//
// Defensive parsing: any unrecognised shape still emits an event with
// the bare "type" + "subtype" so a future claude release adding a new
// event kind doesn't go silently missing from the trace.
// runClaudeStreamJSON accepts an optional sessionID variadic; callers that
// don't thread the session may omit it. The first element, if present, is
// injected as KITSOKI_SESSION_ID per-subprocess (not via os.Setenv).
func runClaudeStreamJSON(ctx context.Context, bin string, cliArgs []string, stdin, workingDir string, sessionID ...string) (ClaudeRun, string, error) {
	sid := ""
	if len(sessionID) > 0 {
		sid = sessionID[0]
	}

	if r := ClaudeRunnerFromContext(ctx); r != nil {
		// Test seam: stub almost certainly emits non-JSONL text. Run
		// it once, parse what we can (zero or more JSONL events), and
		// fall back to using its raw output as the assistant reply.
		cr, err := r(ctx, cliArgs, stdin, workingDir)
		if err != nil {
			return cr, "", err
		}
		reply, parsedSID, rawEvs := parseStreamJSONOutput(ctx, cr.Stdout)
		if strings.TrimSpace(reply) == "" {
			// Fallback: stub emitted plain text. Honor the legacy
			// buffered-text contract so the existing fake-claude
			// harness keeps producing usable replies.
			reply = cr.Stdout
		}
		cr.Stdout = reply
		cr.RawEvents = rawEvs
		return cr, parsedSID, nil
	}

	cmd := exec.CommandContext(ctx, bin, cliArgs...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Dir = workingDir
	cmd.Env = envWithSessionID(envWithKitsokiBinOnPath(os.Environ()), sid)

	stdoutPipe, pipeErr := cmd.StdoutPipe()
	if pipeErr != nil {
		return ClaudeRun{Infra: pipeErr}, "", nil
	}
	var se strings.Builder
	cmd.Stderr = &se

	if startErr := cmd.Start(); startErr != nil {
		return ClaudeRun{Infra: startErr}, "", nil
	}

	// Read stdout line by line, emitting an event per parseable JSON
	// line and accumulating text content + the final reply string.
	scanner := bufio.NewScanner(stdoutPipe)
	// claude can emit very long single lines (a fat tool_use input,
	// for example). Lift the per-line buffer from 64 KiB to 8 MiB to
	// match what real sessions produce in practice.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	var (
		assembledText strings.Builder
		finalResult   string
		parsedSID     string
		sawAnyJSON    bool
		rawLines      strings.Builder
		rawEvents     []json.RawMessage
	)
	for scanner.Scan() {
		line := scanner.Text()
		rawLines.WriteString(line)
		rawLines.WriteByte('\n')

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var ev map[string]any
		if jErr := json.Unmarshal([]byte(trimmed), &ev); jErr != nil {
			// Not a JSON line — could be a transient claude diagnostic
			// or a trailing newline. Skip silently; we'll fall back to
			// raw text if no JSON ever lands.
			continue
		}
		sawAnyJSON = true
		rawEvents = append(rawEvents, json.RawMessage(trimmed))
		emitStreamEvent(ctx, ev)
		text, tool, isResult, resultText, sid := classifyStreamEvent(ev)
		_ = tool
		if text != "" {
			assembledText.WriteString(text)
		}
		if isResult && resultText != "" {
			finalResult = resultText
		}
		if sid != "" && parsedSID == "" {
			parsedSID = sid
		}
	}
	// scanner.Err() can return a "token too long" error if a single
	// JSON line exceeds the 8 MiB cap. Surface that so the caller
	// doesn't silently truncate the reply.
	scanErr := scanner.Err()

	waitErr := cmd.Wait()

	cr := ClaudeRun{Stderr: se.String(), RawEvents: rawEvents}
	if waitErr != nil {
		if ctx.Err() != nil {
			return ClaudeRun{}, parsedSID, ctx.Err()
		}
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			cr.ExitCode = exitErr.ExitCode()
		} else {
			cr.Infra = waitErr
		}
	}
	if scanErr != nil && cr.Infra == nil {
		cr.Infra = fmt.Errorf("read stream-json stdout: %w", scanErr)
	}

	// Synthesize the final reply text. Prefer the `result` event's
	// `result` field; fall back to the accumulated assistant text
	// blocks; fall back to the raw stdout if neither produced
	// anything (e.g. binary emitted no JSONL — covers manual
	// invocations where someone forgot --verbose).
	reply := finalResult
	if strings.TrimSpace(reply) == "" {
		reply = assembledText.String()
	}
	if strings.TrimSpace(reply) == "" && !sawAnyJSON {
		reply = rawLines.String()
	}
	cr.Stdout = strings.TrimRight(reply, "\n")
	return cr, parsedSID, nil
}

// emitStreamEvent surfaces one parsed stream-json event into slog so
// it is visible in the session log as it happens. We keep each record
// tiny (type/subtype/tool/preview) — full content blocks would dwarf
// the log; the preview is enough to see the agent making progress.
//
// When a StreamSink is installed on ctx (the TUI's metaSendCmd wires
// one in via WithStreamSink), the same payload is also forwarded to
// the sink so the chat transcript can render live progress lines.
// The sink call happens BEFORE the slog emit so a panicking sink can't
// swallow the trace record — though sink implementations are required
// to be non-blocking and non-panicking by contract.
func emitStreamEvent(ctx context.Context, ev map[string]any) {
	evType, _ := ev["type"].(string)
	subtype, _ := ev["subtype"].(string)
	text, tool, isResult, resultText, sessionID := classifyStreamEvent(ev)

	preview := text
	if isResult && resultText != "" {
		preview = resultText
	}
	preview = onelinePreview(preview, 120)

	// Tee to the TUI sink, if any. Mirrors the slog attrs below in
	// structured form so the consumer doesn't have to re-parse strings.
	if sink := StreamSinkFrom(ctx); sink != nil {
		out := StreamEvent{
			Type:      evType,
			Subtype:   subtype,
			Tool:      tool,
			Preview:   preview,
			SessionID: sessionID,
			IsResult:  isResult,
		}
		if isResult {
			if cost, ok := ev["total_cost_usd"].(float64); ok {
				out.CostUSD = cost
			}
		}
		sink.OnStreamEvent(ctx, out)
	}

	attrs := []any{
		"type", evType,
	}
	if subtype != "" {
		attrs = append(attrs, "subtype", subtype)
	}
	if tool != "" {
		attrs = append(attrs, "tool", tool)
	}
	if preview != "" {
		attrs = append(attrs, "preview", preview)
	}
	if isResult {
		if cost, ok := ev["total_cost_usd"].(float64); ok {
			attrs = append(attrs, "total_cost_usd", cost)
		}
		if isErr, ok := ev["is_error"].(bool); ok {
			attrs = append(attrs, "is_error", isErr)
		}
		if sessionID != "" {
			attrs = append(attrs, "session_id", sessionID)
		}
	}
	slog.InfoContext(ctx, "metamode.oracle.event", attrs...)
}

// classifyStreamEvent extracts the bits the caller and the logger need
// from one parsed event: any text content, any tool_use tool name,
// whether this is the terminal `result` event, the `result.result`
// field text, and any session_id surfaced by the event. Best-effort and
// defensive — unknown shapes return zero values.
func classifyStreamEvent(ev map[string]any) (text, tool string, isResult bool, resultText, sessionID string) {
	evType, _ := ev["type"].(string)
	if sid, _ := ev["session_id"].(string); sid != "" {
		sessionID = sid
	}
	switch evType {
	case "system":
		// system.init carries session_id (already pulled above).
	case "assistant", "user":
		msg, _ := ev["message"].(map[string]any)
		contentRaw, _ := msg["content"].([]any)
		var firstText, firstTool string
		for _, c := range contentRaw {
			block, _ := c.(map[string]any)
			btype, _ := block["type"].(string)
			switch btype {
			case "text":
				if t, _ := block["text"].(string); t != "" && firstText == "" {
					firstText = t
				}
			case "tool_use":
				if n, _ := block["name"].(string); n != "" && firstTool == "" {
					firstTool = n
					if firstText == "" {
						input, _ := block["input"].(map[string]any)
						firstText = toolUseArgsPreview(n, input)
					}
				}
			case "tool_result":
				if firstText == "" {
					// tool_result content can be a string or a list of
					// {type:text,text:…} blocks. Surface a short preview
					// either way.
					switch v := block["content"].(type) {
					case string:
						firstText = v
					case []any:
						for _, sub := range v {
							sb, _ := sub.(map[string]any)
							if t, _ := sb["text"].(string); t != "" {
								firstText = t
								break
							}
						}
					}
				}
			}
		}
		text = firstText
		tool = firstTool
	case "result":
		isResult = true
		if r, _ := ev["result"].(string); r != "" {
			resultText = r
		}
	}
	return text, tool, isResult, resultText, sessionID
}

// toolUseArgsPreview extracts a short, human-readable summary of a
// tool_use block's input arguments. The result feeds the per-event
// `preview` field, so the TUI renders e.g. "Read prompt.md" /
// "Bash ls flows/" / "Grep -rn 'foo' ." instead of a bare tool
// name. Best-effort and defensive — unknown shapes return "".
//
// The arg keys looked up are the canonical Anthropic tool input
// schemas (Read.file_path, Bash.command, Grep.pattern, Edit.file_path,
// Write.file_path, Glob.pattern, …). For any other tool we fall back
// to the first string-valued field in declaration-stable order.
func toolUseArgsPreview(name string, input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	str := func(k string) string {
		v, _ := input[k].(string)
		return strings.TrimSpace(v)
	}
	// Per-tool primary-arg shortcuts. Keep concise — the line ends up
	// prefixed with "→ <tool> " in the transcript.
	switch name {
	case "Bash":
		return str("command")
	case "Read", "Edit", "Write", "NotebookEdit":
		return str("file_path")
	case "Glob":
		if p := str("pattern"); p != "" {
			if path := str("path"); path != "" {
				return p + " (in " + path + ")"
			}
			return p
		}
	case "Grep":
		pat := str("pattern")
		if pat == "" {
			break
		}
		var extras []string
		if path := str("path"); path != "" {
			extras = append(extras, "in "+path)
		}
		if glob := str("glob"); glob != "" {
			extras = append(extras, glob)
		}
		if len(extras) > 0 {
			return pat + " (" + strings.Join(extras, ", ") + ")"
		}
		return pat
	case "WebFetch":
		return str("url")
	case "WebSearch":
		return str("query")
	case "Task", "Agent":
		if d := str("description"); d != "" {
			return d
		}
		return str("prompt")
	case "TodoWrite":
		// Surface the count of todos; the array values are too big.
		if todos, ok := input["todos"].([]any); ok {
			return fmt.Sprintf("%d todos", len(todos))
		}
	}
	// Fallback: first non-empty string field, in alpha-sorted key order
	// for deterministic output. Skip very long values (likely a body
	// blob) — onelinePreview will truncate, but a 10KB pongo template
	// pasted as a tool arg shouldn't make the log line obnoxious.
	keys := make([]string, 0, len(input))
	for k := range input {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, _ := input[k].(string); s != "" {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			return s
		}
	}
	return ""
}

// onelinePreview collapses whitespace and truncates to n runes so a
// streamed event's text content fits cleanly into the slog trace.
func onelinePreview(s string, n int) string {
	if s == "" {
		return ""
	}
	// Collapse newlines / tabs to single spaces.
	repl := strings.NewReplacer("\r\n", " ", "\n", " ", "\t", " ", "\r", " ")
	s = repl.Replace(s)
	s = strings.TrimSpace(s)
	if n <= 0 {
		return s
	}
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// parseStreamJSONOutput parses a possibly-buffered chunk of stream-json
// output (one event per line) without forking a subprocess. Used by
// the test-stub fallback in runClaudeStreamJSON. Returns the reply
// text, the session ID from system.init, and all parsed raw events.
func parseStreamJSONOutput(ctx context.Context, raw string) (reply, sessionID string, rawEvents []json.RawMessage) {
	var assembled strings.Builder
	var finalResult string
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if jErr := json.Unmarshal([]byte(line), &ev); jErr != nil {
			continue
		}
		rawEvents = append(rawEvents, json.RawMessage(line))
		emitStreamEvent(ctx, ev)
		text, _, isResult, resultText, sid := classifyStreamEvent(ev)
		if text != "" {
			assembled.WriteString(text)
		}
		if isResult && resultText != "" {
			finalResult = resultText
		}
		if sid != "" && sessionID == "" {
			sessionID = sid
		}
	}
	reply = finalResult
	if strings.TrimSpace(reply) == "" {
		reply = assembled.String()
	}
	return reply, sessionID, rawEvents
}

// claudeExitErrorMessage builds the Result.Error string for a non-zero
// claude exit, preferring stderr text, then stdout, then a fallback.
func claudeExitErrorMessage(exitCode int, stderr, stdout string) string {
	if s := strings.TrimSpace(stderr); s != "" {
		return s
	}
	if stdout != "" {
		return stdout
	}
	return fmt.Sprintf("claude exited with code %d", exitCode)
}

// envWithKitsokiBinOnPath returns a copy of env with the kitsoki
// binary's directory prepended to PATH. Idempotent: if PATH already
// starts with that directory the env is returned unchanged.
//
// The directory comes from os.Executable(). Under `go run` this is
// a per-invocation temp build artefact (e.g.
// /tmp/go-build3210293/.../exe/kitsoki) whose basename is "kitsoki"
// — exactly what an agent prompt that says `kitsoki bug create …`
// resolves. Under `go install` or a packaged build it's the
// install/release path; same shape.
//
// On platforms where os.Executable() fails (rare; unsupported OS or
// the parent removed the exe file) the function returns env
// unchanged. The downstream "command not found" error remains
// informative.
func envWithKitsokiBinOnPath(env []string) []string {
	self, err := os.Executable()
	if err != nil || self == "" {
		return env
	}
	dir := filepath.Dir(self)
	if dir == "" || dir == "." {
		return env
	}
	sep := string(os.PathListSeparator)
	prefix := dir + sep
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			found = true
			existing := strings.TrimPrefix(kv, "PATH=")
			if existing == dir || strings.HasPrefix(existing, prefix) {
				// Already first — no need to touch.
				out = append(out, kv)
				continue
			}
			out = append(out, "PATH="+dir+sep+existing)
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}

// envWithSessionID returns a copy of env with KITSOKI_SESSION_ID set to
// sessionID. When sessionID is empty the env is returned unchanged so
// callers don't need to guard the call site. This is the per-subprocess
// equivalent of os.Setenv — it does NOT mutate the process-global env.
func envWithSessionID(env []string, sessionID string) []string {
	if sessionID == "" {
		return env
	}
	const key = "KITSOKI_SESSION_ID"
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			out = append(out, key+"="+sessionID)
			found = true
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, key+"="+sessionID)
	}
	return out
}

// resolveOracleBin returns the path to the claude binary, honoring
// OracleBinEnv and falling back to a PATH lookup. Returns
// ErrOracleUnavailable if neither is set.
//
// When a ClaudeRunner is installed in ctx (tests) the function
// short-circuits with a sentinel path because the runner stub does
// not need a real binary on disk.
func resolveOracleBin(ctx context.Context) (string, error) {
	if ClaudeRunnerFromContext(ctx) != nil {
		return "stub://claude", nil
	}
	if bin := os.Getenv(OracleBinEnv); bin != "" {
		return bin, nil
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", ErrOracleUnavailable
	}
	return path, nil
}
