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
	"time"
)

// claudeTranscriptFormat is the Format stamped on the transcript sidecar and
// TranscriptRef for the in-host claude path. The sidecar holds the claude
// stream-json events byte-verbatim, one per line, so an off-the-shelf claude
// jsonl parser can consume it unchanged. See the agent-action-transcripts docs.
const claudeTranscriptFormat = "claude-stream-json"

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
	// Usage is the token-usage object from the terminal `result` event
	// (input_tokens, output_tokens, cache_read_input_tokens,
	// cache_creation_input_tokens, …) reported by the claude CLI. It is
	// nil when the run produced no result event carrying usage — e.g. a
	// plain-text test stub, or a run that errored before completion. The
	// transport also stashes this into the per-call usage box (see
	// recordOracleUsage) so OracleReturned events surface per-invocation
	// tokens without every verb handler threading a ClaudeRun through its
	// (sometimes deep) call tree.
	Usage map[string]any
	// CostUSD is the result event's total_cost_usd (0 when absent).
	CostUSD float64
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

// runClaudeOneShot dispatches a claude "one-shot" call.
//
// "stream-json everywhere": rather than ask the CLI for buffered --output-format
// text (which carries NO token usage and emits no live events), this path now
// runs the call as stream-json — capturing per-invocation token usage and
// teeing progress events to slog / any installed StreamSink — while still
// returning the assistant's final reply as ClaudeRun.Stdout. The one exception
// is an explicit --output-format json request (the envelope contract used by
// ask_with_mcp's stdout_json binding): that stays buffered so the caller still
// receives the full result envelope to unmarshal, and usage is extracted from
// the envelope instead.
//
// When a ClaudeRunner is installed in ctx (test setup) the stub is invoked
// in-process by whichever underlying runner this dispatches to.
//
// The trailing-newline trim on Stdout is enforced here, not in the runner
// implementations, so callers (and downstream code that splits on \n) never
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

	// Non-claude backends (copilot) expose only one JSON mode — JSONL, one
	// event per line — so there is no separate buffered-envelope contract to
	// honor. Route every one-shot through the streaming parser, which already
	// synthesizes the final reply text and captures usage from the JSONL.
	// TranslateInvocation (inside runClaudeStreamJSON) rewrites the claude argv
	// the caller built into the backend's real flags.
	if OracleBackendFromContext(ctx).Name() != "claude" {
		cr, _, err := runClaudeStreamJSON(ctx, bin, cliArgs, stdin, workingDir, sid)
		cr.Stdout = strings.TrimRight(cr.Stdout, "\n")
		return cr, err
	}

	// Explicit --output-format json: preserve the buffered envelope contract.
	if requestedOutputFormat(cliArgs) == "json" {
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
		// The envelope carries usage + cost; surface them like the stream path.
		if usage, cost := parseEnvelopeUsage(cr.Stdout); usage != nil || cost != 0 {
			cr.Usage = usage
			cr.CostUSD = cost
			recordOracleUsage(ctx, usage, cost)
		}
		return cr, err
	}

	// Everything else (text / stream-json / unset) runs as stream-json so the
	// call captures usage and emits live events. runClaudeStreamJSON honours the
	// same ClaudeRunner stub and applies its own trailing-newline trim; we trim
	// again here (idempotent) to keep the contract obvious at the dispatcher.
	cr, _, err := runClaudeStreamJSON(ctx, bin, forceStreamJSONArgs(cliArgs), stdin, workingDir, sid)
	cr.Stdout = strings.TrimRight(cr.Stdout, "\n")
	return cr, err
}

// requestedOutputFormat returns the value of the --output-format flag in
// cliArgs (handling both "--output-format x" and "--output-format=x"), or ""
// when the flag is absent.
func requestedOutputFormat(cliArgs []string) string {
	for i, a := range cliArgs {
		if a == "--output-format" {
			if i+1 < len(cliArgs) {
				return cliArgs[i+1]
			}
			return ""
		}
		if v, ok := strings.CutPrefix(a, "--output-format="); ok {
			return v
		}
	}
	return ""
}

// forceStreamJSONArgs returns a copy of cliArgs with the output format pinned
// to stream-json and --verbose present (claude requires --verbose alongside
// --output-format stream-json in -p mode). Any existing --output-format value
// is rewritten; if none is present one is appended.
func forceStreamJSONArgs(cliArgs []string) []string {
	out := make([]string, 0, len(cliArgs)+3)
	hasFormat, hasVerbose := false, false
	for i := 0; i < len(cliArgs); i++ {
		a := cliArgs[i]
		switch {
		case a == "--output-format":
			hasFormat = true
			out = append(out, "--output-format", "stream-json")
			i++ // skip the old value
		case strings.HasPrefix(a, "--output-format="):
			hasFormat = true
			out = append(out, "--output-format=stream-json")
		default:
			if a == "--verbose" {
				hasVerbose = true
			}
			out = append(out, a)
		}
	}
	if !hasFormat {
		out = append(out, "--output-format", "stream-json")
	}
	if !hasVerbose {
		out = append(out, "--verbose")
	}
	return out
}

// parseEnvelopeUsage extracts the token-usage object and total_cost_usd from a
// buffered --output-format json result envelope. Returns (nil, 0) when stdout
// is not a parseable result envelope (e.g. a plain-text test stub).
func parseEnvelopeUsage(stdout string) (map[string]any, float64) {
	if strings.TrimSpace(stdout) == "" {
		return nil, 0
	}
	var env map[string]any
	if json.Unmarshal([]byte(stdout), &env) != nil {
		return nil, 0
	}
	usage, _ := env["usage"].(map[string]any)
	cost, _ := env["total_cost_usd"].(float64)
	return usage, cost
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
	cmd.Env = envWithProvider(envWithSessionID(envWithKitsokiBinOnPath(os.Environ()), sessionID), OracleProviderEnvFromCtx(ctx))
	// When kitsoki holds the one IDE link, scrub the auto-connect signals so the
	// inner claude doesn't open its own socket (shared decision #1). No-op (env
	// untouched) when no link is connected. Outermost wrap so it sees the
	// port-bearing entry to drop.
	if l := IDELinkFromContext(ctx); l != nil && l.Connected() {
		cmd.Env = envScrubIDE(cmd.Env)
	}

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

	backend := OracleBackendFromContext(ctx)
	inv := backend.TranslateInvocation(cliArgs, stdin, workingDir)

	if r := backend.runnerFromContext(ctx); r != nil {
		// Test seam: stub almost certainly emits non-JSONL text. Run
		// it once (with the backend's translated argv/stdin), parse what
		// we can (zero or more JSONL events), and fall back to using its
		// raw output as the assistant reply.
		cr, err := r(ctx, inv.Args, inv.Stdin, inv.WorkingDir)
		if err != nil {
			return cr, "", err
		}
		reply, parsedSID, rawEvs, usage, cost := parseStreamJSONOutput(ctx, cr.Stdout)
		if strings.TrimSpace(reply) == "" {
			// Fallback: stub emitted plain text. Honor the legacy
			// buffered-text contract so the existing fake-claude
			// harness keeps producing usable replies.
			reply = cr.Stdout
		}
		cr.Stdout = reply
		cr.RawEvents = rawEvs
		cr.Usage = usage
		cr.CostUSD = cost
		recordOracleUsage(ctx, usage, cost)
		return cr, parsedSID, nil
	}

	cmd := exec.CommandContext(ctx, bin, inv.Args...)
	cmd.Stdin = strings.NewReader(inv.Stdin)
	cmd.Dir = inv.WorkingDir
	cmd.Env = envWithProvider(envWithSessionID(envWithKitsokiBinOnPath(os.Environ()), sid), OracleProviderEnvFromCtx(ctx))
	// IDE auto-connect scrub (shared decision #1) — outermost wrap, gated on a
	// connected link in ctx; no-op otherwise so the env is byte-identical to
	// today on every headless/flow path.
	if l := IDELinkFromContext(ctx); l != nil && l.Connected() {
		cmd.Env = envScrubIDE(cmd.Env)
	}

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

	// Transcript tee (agent-action-transcripts): when a TranscriptWriter and a
	// call_id are both installed in ctx, each raw stream-json event is appended
	// verbatim to the per-call sidecar with a capture-time offset (ms since the
	// call started). The bytes are byte-identical to what RawEvents holds. The
	// wall clock here is recorded and replayed, never re-derived (see the
	// proposal's Determinism section). A nil writer / empty call_id is a no-op.
	transcriptW := TranscriptWriterFrom(ctx)
	transcriptCallID := CallIDFrom(ctx)
	teeTranscript := transcriptW != nil && transcriptCallID != ""
	// Offset events from the shared call-start when one is installed (a decide
	// call's several --resume subprocesses share it, so the waterfall stays
	// monotonic), else from this invocation's start (the single-session case).
	callStart := time.Now()
	if t, ok := CallStartFrom(ctx); ok {
		callStart = t
	}

	var (
		assembledText strings.Builder
		finalResult   string
		parsedSID     string
		sawAnyJSON    bool
		rawLines      strings.Builder
		rawEvents     []json.RawMessage
		resultUsage   map[string]any
		resultCost    float64
		sumOutTokens  int
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
		if teeTranscript {
			transcriptW.Append(transcriptCallID, backend.TranscriptFormat(),
				json.RawMessage(trimmed), time.Since(callStart).Milliseconds())
		}
		ce := backend.Classify(ev)
		emitClassified(ctx, ce)
		sumOutTokens += ce.OutputTokens
		if ce.Text != "" {
			assembledText.WriteString(ce.Text)
		}
		if ce.IsResult {
			if ce.ResultText != "" {
				finalResult = ce.ResultText
			}
			// The terminal result event carries the authoritative
			// cumulative token usage + cost for the whole turn.
			if ce.Usage != nil || ce.Cost != 0 {
				resultUsage, resultCost = ce.Usage, ce.Cost
			}
		}
		// Copilot streams the final reply incrementally and never stamps a
		// dedicated result-text field; keep the latest non-empty assistant
		// message as the running reply so the fallback below picks it up.
		if !ce.IsResult && ce.Type == "assistant.message" && ce.Text != "" {
			finalResult = ce.Text
		}
		if ce.SessionID != "" && parsedSID == "" {
			parsedSID = ce.SessionID
		}
	}
	resultUsage = mergeOutputTokens(resultUsage, sumOutTokens)
	// scanner.Err() can return a "token too long" error if a single
	// JSON line exceeds the 8 MiB cap. Surface that so the caller
	// doesn't silently truncate the reply.
	scanErr := scanner.Err()

	waitErr := cmd.Wait()

	cr := ClaudeRun{Stderr: se.String(), RawEvents: rawEvents, Usage: resultUsage, CostUSD: resultCost}
	recordOracleUsage(ctx, resultUsage, resultCost)
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
func emitClassified(ctx context.Context, ce classifiedEvent) {
	// preview is the compact, single-line value for the slog trace and
	// the tool-use breadcrumb: tool args when this is a tool_use event,
	// otherwise a clipped peek at the narration / result text. Full
	// content blocks would dwarf the log.
	previewSrc := ce.ToolArgs
	if previewSrc == "" {
		previewSrc = ce.Text
	}
	if ce.IsResult && ce.ResultText != "" {
		previewSrc = ce.ResultText
	}
	preview := onelinePreview(previewSrc, 120)

	// Tee to the TUI sink, if any. Mirrors the slog attrs below in
	// structured form so the consumer doesn't have to re-parse strings.
	if sink := StreamSinkFrom(ctx); sink != nil {
		out := StreamEvent{
			Type:    ce.Type,
			Subtype: ce.Subtype,
			Tool:    ce.Tool,
			Preview: preview,
			Tools:   ce.Tools,
			// Full narration prose, untruncated — the transcript word-
			// wraps it. Cutting it mid-sentence was the truncation bug.
			Text:      ce.Text,
			SessionID: ce.SessionID,
			IsResult:  ce.IsResult,
		}
		if ce.IsResult {
			out.CostUSD = ce.Cost
			if ce.Usage != nil {
				out.InputTokens = usageInt(ce.Usage, "input_tokens")
				out.OutputTokens = usageInt(ce.Usage, "output_tokens")
				out.CacheReadTokens = usageInt(ce.Usage, "cache_read_input_tokens")
				out.CacheCreationTokens = usageInt(ce.Usage, "cache_creation_input_tokens")
			}
		}
		sink.OnStreamEvent(ctx, out)
	}

	attrs := []any{
		"type", ce.Type,
	}
	if ce.Subtype != "" {
		attrs = append(attrs, "subtype", ce.Subtype)
	}
	if ce.Tool != "" {
		attrs = append(attrs, "tool", ce.Tool)
	}
	if preview != "" {
		attrs = append(attrs, "preview", preview)
	}
	if ce.IsResult {
		if ce.Cost != 0 {
			attrs = append(attrs, "total_cost_usd", ce.Cost)
		}
		if ce.IsError {
			attrs = append(attrs, "is_error", ce.IsError)
		}
		if ce.SessionID != "" {
			attrs = append(attrs, "session_id", ce.SessionID)
		}
		if ce.Usage != nil {
			attrs = append(attrs,
				"input_tokens", usageInt(ce.Usage, "input_tokens"),
				"output_tokens", usageInt(ce.Usage, "output_tokens"),
				"cache_read_input_tokens", usageInt(ce.Usage, "cache_read_input_tokens"),
				"cache_creation_input_tokens", usageInt(ce.Usage, "cache_creation_input_tokens"),
			)
		}
	}
	slog.InfoContext(ctx, "metamode.oracle.event", attrs...)
}

// mergeOutputTokens injects a summed per-message output-token count into a
// terminal usage map when that map carries no output_tokens of its own. This is
// the copilot case: its result event reports premium_requests + durations but no
// token totals, while each assistant.message carries an outputTokens count. For
// claude (whose result usage already has output_tokens) and for any run with no
// per-message tokens (sum == 0) the usage map is returned unchanged, so the
// behavior is byte-identical on the existing path. A nil usage map with a
// positive sum is materialized so the count is not lost.
func mergeOutputTokens(usage map[string]any, sumOutTokens int) map[string]any {
	if sumOutTokens <= 0 {
		return usage
	}
	if usage == nil {
		return map[string]any{"output_tokens": float64(sumOutTokens)}
	}
	if _, present := usage["output_tokens"]; !present {
		usage["output_tokens"] = float64(sumOutTokens)
	}
	return usage
}

// usageInt reads a token-count field from a claude usage object. JSON numbers
// unmarshal as float64; this rounds to the nearest int. Returns 0 when the key
// is absent or not numeric.
func usageInt(usage map[string]any, key string) int {
	switch v := usage[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// classifyStreamEvent extracts the bits the caller and the logger need
// from one parsed event: the assistant narration prose (text), any
// tool_use tool name plus a compact preview of its args (toolArgs),
// whether this is the terminal `result` event, the `result.result`
// field text, and any session_id surfaced by the event. Best-effort and
// defensive — unknown shapes return zero values.
//
// text is the FULL narration, joined across every text block and never
// truncated — a single assistant message commonly carries a thought
// AND a tool call, and the thought must survive intact for the
// transcript. toolArgs is kept separate (and compact) so the two never
// clobber each other. When a message has no prose but does carry a
// tool_result (a "user" event), its content backfills text so the slog
// trace and reply-assembly fallback still see something.
func classifyStreamEvent(ev map[string]any) (text, tool, toolArgs string, isResult bool, resultText, sessionID string) {
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
		var texts []string
		var firstTool, firstResult string
		for _, c := range contentRaw {
			block, _ := c.(map[string]any)
			btype, _ := block["type"].(string)
			switch btype {
			case "text":
				if t, _ := block["text"].(string); t != "" {
					texts = append(texts, t)
				}
			case "tool_use":
				if n, _ := block["name"].(string); n != "" && firstTool == "" {
					firstTool = n
					input, _ := block["input"].(map[string]any)
					toolArgs = toolUseArgsPreview(n, input)
				}
			case "tool_result":
				if firstResult == "" {
					// tool_result content can be a string or a list of
					// {type:text,text:…} blocks. Surface a short preview
					// either way.
					switch v := block["content"].(type) {
					case string:
						firstResult = v
					case []any:
						for _, sub := range v {
							sb, _ := sub.(map[string]any)
							if t, _ := sb["text"].(string); t != "" {
								firstResult = t
								break
							}
						}
					}
				}
			}
		}
		text = strings.Join(texts, "\n")
		if text == "" {
			// No narration prose — fall back to the tool_result content
			// so "user" events still carry a preview for the trace.
			text = firstResult
		}
		tool = firstTool
	case "result":
		isResult = true
		if r, _ := ev["result"].(string); r != "" {
			resultText = r
		}
	}
	return text, tool, toolArgs, isResult, resultText, sessionID
}

// assistantToolUses returns EVERY tool_use block in an assistant event,
// in declaration order, each with a compact one-line args preview. Where
// classifyStreamEvent surfaces only the first tool (for the scalar
// StreamEvent.Tool / slog breadcrumb), this is what lets a consumer
// render each parallel tool call on its own line. Returns nil for any
// non-assistant event or one carrying no tool_use blocks. Best-effort
// and defensive — unknown shapes are skipped.
func assistantToolUses(ev map[string]any) []StreamToolUse {
	if t, _ := ev["type"].(string); t != "assistant" {
		return nil
	}
	msg, _ := ev["message"].(map[string]any)
	content, _ := msg["content"].([]any)
	var tools []StreamToolUse
	for _, c := range content {
		block, _ := c.(map[string]any)
		if bt, _ := block["type"].(string); bt != "tool_use" {
			continue
		}
		name, _ := block["name"].(string)
		if name == "" {
			continue
		}
		input, _ := block["input"].(map[string]any)
		tools = append(tools, StreamToolUse{
			Name:    name,
			Preview: onelinePreview(toolUseArgsPreview(name, input), 120),
		})
	}
	return tools
}

// resultEventUsage extracts the token-usage object and total_cost_usd from a
// terminal `result` event. The usage object is claude's cumulative per-turn
// total (input_tokens, output_tokens, cache_read_input_tokens,
// cache_creation_input_tokens, …). Returns (nil, 0) for non-result events or
// events that carry no usage. Best-effort and defensive.
func resultEventUsage(ev map[string]any) (map[string]any, float64) {
	usage, _ := ev["usage"].(map[string]any)
	cost, _ := ev["total_cost_usd"].(float64)
	return usage, cost
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
// text, the session ID from system.init, all parsed raw events, and the
// token usage + cost from the terminal result event (nil/0 when the
// chunk carries no result usage, e.g. a plain-text stub).
func parseStreamJSONOutput(ctx context.Context, raw string) (reply, sessionID string, rawEvents []json.RawMessage, usage map[string]any, cost float64) {
	var assembled strings.Builder
	var finalResult string
	// Transcript tee (agent-action-transcripts): mirror the live subprocess path
	// so a stubbed stream-json run (the test seam) also captures the sidecar.
	// No-op when no writer / call_id is installed.
	transcriptW := TranscriptWriterFrom(ctx)
	transcriptCallID := CallIDFrom(ctx)
	teeTranscript := transcriptW != nil && transcriptCallID != ""
	callStart := time.Now()
	if t, ok := CallStartFrom(ctx); ok {
		callStart = t
	}
	backend := OracleBackendFromContext(ctx)
	sumOutTokens := 0
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
		if teeTranscript {
			transcriptW.Append(transcriptCallID, backend.TranscriptFormat(),
				json.RawMessage(line), time.Since(callStart).Milliseconds())
		}
		ce := backend.Classify(ev)
		emitClassified(ctx, ce)
		sumOutTokens += ce.OutputTokens
		if ce.Text != "" {
			assembled.WriteString(ce.Text)
		}
		if ce.IsResult {
			if ce.ResultText != "" {
				finalResult = ce.ResultText
			}
			if ce.Usage != nil || ce.Cost != 0 {
				usage, cost = ce.Usage, ce.Cost
			}
		}
		if !ce.IsResult && ce.Type == "assistant.message" && ce.Text != "" {
			finalResult = ce.Text
		}
		if ce.SessionID != "" && sessionID == "" {
			sessionID = ce.SessionID
		}
	}
	usage = mergeOutputTokens(usage, sumOutTokens)
	reply = finalResult
	if strings.TrimSpace(reply) == "" {
		reply = assembled.String()
	}
	return reply, sessionID, rawEvents, usage, cost
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

// envWithProvider returns a copy of env with each provider override applied:
// for every KEY in provEnv, any existing KEY= entry is replaced (last wins),
// and absent keys are appended. When provEnv is empty the input env is returned
// unchanged so the non-provider path is byte-identical to today's behavior.
//
// This is how a selected provider points the `claude` subprocess at an
// alternate backend — typically ANTHROPIC_BASE_URL / ANTHROPIC_AUTH_TOKEN /
// NODE_EXTRA_CA_CERTS — without mutating the process-global environment, so
// concurrent invocations on different providers don't race.
func envWithProvider(env []string, provEnv map[string]string) []string {
	if len(provEnv) == 0 {
		return env
	}
	// Deterministic key order so the produced env slice is stable across runs
	// (tests assert on it; nondeterministic map iteration would flake).
	keys := make([]string, 0, len(provEnv))
	for k := range provEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(env)+len(provEnv))
	// Track which provider keys still need appending after the rewrite pass.
	remaining := make(map[string]bool, len(provEnv))
	for _, k := range keys {
		remaining[k] = true
	}
	for _, kv := range env {
		replaced := false
		for _, k := range keys {
			if strings.HasPrefix(kv, k+"=") {
				out = append(out, k+"="+provEnv[k])
				remaining[k] = false
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, kv)
		}
	}
	for _, k := range keys {
		if remaining[k] {
			out = append(out, k+"="+provEnv[k])
		}
	}
	return out
}

// resolveOracleBin returns the path to the binary of the oracle backend
// selected on ctx (claude by default), honoring that backend's bin-override env
// and test-stub seam. Verb handlers call this; the backend owns the details.
func resolveOracleBin(ctx context.Context) (string, error) {
	return OracleBackendFromContext(ctx).ResolveBin(ctx)
}

// resolveClaudeBin returns the path to the claude binary, honoring
// OracleBinEnv and falling back to a PATH lookup. Returns
// ErrOracleUnavailable if neither is set.
//
// When a ClaudeRunner is installed in ctx (tests) the function
// short-circuits with a sentinel path because the runner stub does
// not need a real binary on disk.
func resolveClaudeBin(ctx context.Context) (string, error) {
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
