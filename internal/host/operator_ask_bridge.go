// Package host — operator-ask host-side bridge (phase 3).
//
// This is the host half of the operator-question forwarding wire. When a live
// operator surface is attached (OperatorInteractive), agent dispatch:
//
//  1. starts a per-call unix-socket listener (startOperatorAskListener) that
//     bridges the wire protocol to the in-context OperatorPrompter, and
//  2. attaches the `kitsoki mcp-operator-ask` server (pointed at that socket) to
//     the claude subprocess, adds mcp__operator__ask to --allowedTools, and
//     appends a system-prompt clause telling the agent to use it.
//
// The grandchild MCP server (internal/mcp.OperatorAskServer) dials the socket
// and blocks; this listener reads the forwarded question, calls the prompter
// (which surfaces it on web/TUI and blocks for the operator), and writes the
// answer back. AskUserQuestion stays in alwaysDeniedTools throughout, so
// mcp__operator__ask is the single path an agent can use to reach the operator.
//
// When no operator is attached, attachOperatorAsk is a no-op: the subprocess
// gets neither the tool nor the server, AskUserQuestion stays denied, and the
// model decides on its own (the headless tool-denied posture).
package host

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	kitsokimcp "kitsoki/internal/mcp"
)

// operatorAskToolName is the allowed-tool / mcp__<server>__<tool> identifier the
// agent must call. The server is named "operator" in the MCP config, the tool
// "ask" (OperatorAskServer's default), so the qualified name is this.
const operatorAskToolName = "mcp__operator__ask"

// operatorAskServerName is the MCP server key in the generated --mcp-config; it
// determines the operatorAskToolName prefix.
const operatorAskServerName = "operator"

// operatorAskWaitTimeout bounds how long the host waits for the operator to
// answer a forwarded question before returning an error frame (which the agent
// sees as "proceed using your best judgement"). Generous — the operator may step
// away — but not unbounded, so a runaway agent can't park a turn forever.
const operatorAskWaitTimeout = 10 * time.Minute

// operatorAskSystemClause is appended (via --append-system-prompt) to every
// operator-interactive subprocess so the agent knows the supported channel for
// asking the human.
const operatorAskSystemClause = "To ask the human operator running this session a question you cannot answer " +
	"yourself, call the `" + operatorAskToolName + "` tool with a `questions` array (each question has a " +
	"`header`, the `question` text, and 2–4 `options`, each with a `label` and `description`; set " +
	"`multiSelect: true` to allow multiple selections). It returns the operator's selected option label(s). " +
	"The built-in AskUserQuestion tool is disabled; this is the only way to reach the operator. Use it sparingly " +
	"— only when you genuinely need a decision or clarification."

// operatorAskListener owns a per-call unix socket that bridges the operator-ask
// wire protocol to an OperatorPrompter.
type operatorAskListener struct {
	ln        net.Listener
	sockPath  string
	prompter  OperatorPrompter
	sessionID string
	timeout   time.Duration
}

type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func newPipeListener() *pipeListener {
	return &pipeListener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr("operator-ask-memory") }

func (l *pipeListener) Dial() (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		return nil, net.ErrClosed
	}
}

type pipeAddr string

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return string(a) }

var operatorAskListenerStarter = startOperatorAskListener

func operatorAskSocketDirs() []string {
	if dir := os.Getenv("KITSOKI_OPERATOR_ASK_SOCKET_DIR"); dir != "" {
		return []string{dir}
	}
	candidates := []string{os.TempDir(), "/private/tmp", "/tmp"}
	out := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		// Normalize: macOS os.TempDir() ($TMPDIR) carries a TRAILING SLASH
		// (/var/folders/.../T/), so a downstream `dir + "/"` prefix check would
		// become a double-slash that never matches the filepath.Join-cleaned
		// socket path. Clean strips it so the candidate dir and the bound socket
		// path agree (and so dedup catches /tmp vs /tmp/).
		dir = filepath.Clean(dir)
		if seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}

// startOperatorAskListener binds a unix socket and serves it until close() (or
// ctx cancellation). Each connection carries one question→answer exchange routed
// to prompter. timeout bounds each operator wait; zero uses operatorAskWaitTimeout.
func startOperatorAskListener(ctx context.Context, prompter OperatorPrompter, sessionID string, timeout time.Duration) (*operatorAskListener, error) {
	if prompter == nil {
		return nil, fmt.Errorf("operator-ask: nil prompter")
	}
	if timeout <= 0 {
		timeout = operatorAskWaitTimeout
	}
	// A short temp base plus a truncated uuid keeps the path under the macOS
	// ~104-char sun_path limit. Sandboxed macOS test runners can expose
	// os.TempDir() or /tmp paths that reject Unix socket bind, so try the known
	// writable short bases before failing.
	var (
		sockPath string
		ln       net.Listener
		errs     []string
	)
	for _, dir := range operatorAskSocketDirs() {
		candidate := filepath.Join(dir, "kitsoki-opask-"+newUUID()[:12]+".sock")
		var err error
		ln, err = net.Listen("unix", candidate)
		if err == nil {
			sockPath = candidate
			break
		}
		errs = append(errs, fmt.Sprintf("%s: %v", candidate, err))
	}
	if ln == nil {
		return nil, fmt.Errorf("operator-ask: listen: %s", strings.Join(errs, "; "))
	}
	return startOperatorAskListenerOn(ctx, ln, sockPath, prompter, sessionID, timeout), nil
}

func startOperatorAskListenerOn(ctx context.Context, ln net.Listener, sockPath string, prompter OperatorPrompter, sessionID string, timeout time.Duration) *operatorAskListener {
	l := &operatorAskListener{ln: ln, sockPath: sockPath, prompter: prompter, sessionID: sessionID, timeout: timeout}
	// Close the listener when the dispatch ctx is cancelled so a parked turn
	// (operator never answers, session torn down) unblocks.
	stop := context.AfterFunc(ctx, func() { _ = ln.Close() })
	go func() {
		defer stop()
		l.serve(ctx)
	}()
	return l
}

// close stops the accept loop and removes the socket file.
func (l *operatorAskListener) close() {
	_ = l.ln.Close()
	_ = os.Remove(l.sockPath)
}

// serve accepts connections until the listener is closed.
func (l *operatorAskListener) serve(ctx context.Context) {
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go l.handleConn(ctx, conn)
	}
}

// handleConn reads one OperatorAskRequest, routes it to the prompter under a
// bounded ctx, and writes one OperatorAskResponse.
func (l *operatorAskListener) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req kitsokimcp.OperatorAskRequest
	if err := json.Unmarshal(line, &req); err != nil {
		l.writeResp(conn, kitsokimcp.OperatorAskResponse{Error: "malformed question payload"})
		return
	}

	// questionID correlates this forwarded batch across the asked/answered (or
	// asked/unanswered) pair in the trace. Truncated like the socket-path uuid.
	questionID := newUUID()[:12]

	questions := wireToOperatorQuestions(req.Questions)
	// headers shows WHAT was asked (each question's Header) without leaking the
	// option labels/descriptions into the trace.
	headers := make([]string, 0, len(questions))
	for _, q := range questions {
		headers = append(headers, q.Header)
	}
	slog.InfoContext(ctx, "operator.question.asked",
		"session_id", l.sessionID, "question_id", questionID,
		"questions", len(questions), "headers", headers)

	askCtx, cancel := context.WithTimeout(ctx, l.timeout)
	defer cancel()
	// time.Now() is fine here: this is production host code, not a workflow script.
	start := time.Now()
	answers, askErr := l.prompter.Ask(askCtx, l.sessionID, questions)
	if askErr != nil {
		slog.InfoContext(ctx, "operator.question.unanswered",
			"session_id", l.sessionID, "question_id", questionID,
			"outcome", "unanswered", "duration_ms", time.Since(start).Milliseconds(),
			"error", askErr.Error())
		l.writeResp(conn, kitsokimcp.OperatorAskResponse{Error: askErr.Error()})
		return
	}
	slog.InfoContext(ctx, "operator.question.answered",
		"session_id", l.sessionID, "question_id", questionID,
		"outcome", "answered", "duration_ms", time.Since(start).Milliseconds(),
		"answers", len(answers))
	l.writeResp(conn, kitsokimcp.OperatorAskResponse{Answers: answers})
}

func (l *operatorAskListener) writeResp(conn net.Conn, resp kitsokimcp.OperatorAskResponse) {
	out, err := json.Marshal(resp)
	if err != nil {
		out = []byte(`{"error":"host failed to encode answer"}`)
	}
	_, _ = conn.Write(append(out, '\n'))
}

// wireToOperatorQuestions maps the on-the-wire mcp question shape to the
// host-facing OperatorQuestion type the prompter consumes.
func wireToOperatorQuestions(in []kitsokimcp.OperatorAskQuestion) []OperatorQuestion {
	out := make([]OperatorQuestion, 0, len(in))
	for _, q := range in {
		opts := make([]OperatorOption, 0, len(q.Options))
		for _, o := range q.Options {
			opts = append(opts, OperatorOption{Label: o.Label, Description: o.Description})
		}
		out = append(out, OperatorQuestion{
			Question:    q.Question,
			Header:      q.Header,
			Options:     opts,
			MultiSelect: q.MultiSelect,
		})
	}
	return out
}

// OperatorAskListenerForTest is the exported handle a cross-package test uses to
// drive the real operator-ask listener (StartOperatorAskListenerForTest). It
// proves a new OperatorPrompter implementation lands the same
// operator.question.asked/answered trace events as the TUI/web surfaces, because
// it routes through the identical listener — no in-package duplication of the
// wire/trace logic.
type OperatorAskListenerForTest struct {
	l    *operatorAskListener
	dial func() (net.Conn, error)
}

// StartOperatorAskListenerForTest binds the real per-call operator-ask listener
// over prompter and returns an exported handle. Intended only for tests in other
// packages (the studio prompter's trace test); production wiring uses the
// unexported startOperatorAskListener through attachOperatorAsk.
func StartOperatorAskListenerForTest(ctx context.Context, prompter OperatorPrompter, sessionID string, timeout time.Duration) (*OperatorAskListenerForTest, error) {
	l, err := startOperatorAskListener(ctx, prompter, sessionID, timeout)
	if err != nil {
		return nil, err
	}
	return &OperatorAskListenerForTest{l: l, dial: func() (net.Conn, error) {
		return net.Dial("unix", l.sockPath)
	}}, nil
}

// StartOperatorAskListenerInMemoryForTest serves the same operator-ask listener
// over net.Pipe instead of a filesystem Unix socket. It is for tests in
// sandboxed environments where Unix socket bind is unavailable.
func StartOperatorAskListenerInMemoryForTest(ctx context.Context, prompter OperatorPrompter, sessionID string, timeout time.Duration) (*OperatorAskListenerForTest, error) {
	if prompter == nil {
		return nil, fmt.Errorf("operator-ask: nil prompter")
	}
	if timeout <= 0 {
		timeout = operatorAskWaitTimeout
	}
	ln := newPipeListener()
	l := startOperatorAskListenerOn(ctx, ln, "in-memory", prompter, sessionID, timeout)
	return &OperatorAskListenerForTest{l: l, dial: ln.Dial}, nil
}

// SockPath is the unix socket the listener is bound to (dial it as the grandchild
// mcp-operator-ask server does).
func (h *OperatorAskListenerForTest) SockPath() string { return h.l.sockPath }

// Dial opens a client connection to the test listener. Real listener handles
// dial their Unix socket; in-memory handles return the client end of net.Pipe.
func (h *OperatorAskListenerForTest) Dial() (net.Conn, error) { return h.dial() }

// Close stops the listener and removes the socket file.
func (h *OperatorAskListenerForTest) Close() { h.l.close() }

// attachOperatorAsk wires the operator-ask tool into an agent subprocess WHEN a
// live operator surface is attached. It returns the (possibly extended) CLI args
// and tool list plus a cleanup func the caller MUST defer (always non-nil).
//
// When no operator is attached it is a no-op: cliArgs/tools are returned
// unchanged and cleanup is a no-op, so dispatch keeps the tool-denied posture.
//
// A failure to bind the socket / write the MCP config is logged and treated as
// "no operator" rather than failing the whole agent call — the agent then
// proceeds without the tool, which is strictly better than aborting the turn.
func attachOperatorAsk(ctx context.Context, cliArgs, tools []string) (outArgs, outTools []string, cleanup func(), err error) {
	noop := func() {}
	prompter, ok := OperatorPrompterFrom(ctx)
	if !ok {
		return cliArgs, tools, noop, nil
	}

	sessionID := kitsokiSessionIDFromCtx(ctx)
	l, lerr := operatorAskListenerStarter(ctx, prompter, sessionID, 0)
	if lerr != nil {
		slog.WarnContext(ctx, "operator-ask: listener unavailable; agent will run without the ask tool", "error", lerr)
		return cliArgs, tools, noop, nil
	}

	bin := os.Getenv(kitsokiBinaryEnv)
	if bin == "" {
		exe, exeErr := os.Executable()
		if exeErr != nil {
			l.close()
			slog.WarnContext(ctx, "operator-ask: cannot locate kitsoki binary; agent will run without the ask tool", "error", exeErr)
			return cliArgs, tools, noop, nil
		}
		bin = exe
	}

	server := map[string]any{
		"command": bin,
		"args":    []any{"mcp-operator-ask", "--socket", l.sockPath},
	}
	cfgPath, cfgCleanup, cfgErr := writeMCPConfigTempfile(map[string]any{operatorAskServerName: server}, "kitsoki-opask-mcp")
	if cfgErr != nil {
		l.close()
		slog.WarnContext(ctx, "operator-ask: cannot write MCP config; agent will run without the ask tool", "error", cfgErr)
		return cliArgs, tools, noop, nil
	}

	outArgs = append(cliArgs, "--mcp-config", cfgPath, "--append-system-prompt", operatorAskSystemClause)
	outTools = append(tools, operatorAskToolName)
	cleanup = func() {
		l.close()
		cfgCleanup()
	}
	return outArgs, outTools, cleanup, nil
}
