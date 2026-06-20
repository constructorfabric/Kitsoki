// subprocess.go implements the subprocess JSON-RPC transport.
//
// SubprocessAgent spawns an external binary that speaks a simple JSON-RPC 2.0
// protocol over stdio. Framing is newline-delimited: each request/response is a
// single JSON object terminated by "\n". The method is "agent.ask"; params is
// AskRequest; result is AskResponse.
//
// Wire format example (one-line request, one-line response):
//
//	→ {"jsonrpc":"2.0","id":1,"method":"agent.ask","params":{...AskRequest...}}
//	← {"jsonrpc":"2.0","id":1,"result":{...AskResponse...}}
//
// Error response:
//
//	← {"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"crash reason"}}
//
// Lifecycle:
//   - Subprocess is spawned lazily on the first Ask call.
//   - Reused for the session lifetime.
//   - On subprocess crash (detected by a broken pipe or EOF before a complete
//     response), the agent records the crash via AgentError and respawns on
//     the next Ask call.
//   - Close() sends SIGTERM; if the process does not exit within
//     SubprocessTerminateTimeout, SIGKILL.
//
// Thread-safety: only one Ask call may be in-flight at a time. Concurrent
// callers serialize through a mutex. (The subprocess has a single stdin/stdout
// pipe; JSON-RPC multiplexing is not used.)

package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// jsonrpcRequest is a JSON-RPC 2.0 request frame.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response frame.
type jsonrpcResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      int              `json:"id"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *jsonrpcErrorObj `json:"error,omitempty"`
}

// jsonrpcErrorObj is the JSON-RPC 2.0 error object.
type jsonrpcErrorObj struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// subprocessState holds the live subprocess and its I/O pipes.
type subprocessState struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
}

// SubprocessAgent implements Agent via JSON-RPC 2.0 over a subprocess's stdio.
// The zero value is not usable — construct it with NewSubprocess. Ask is
// serialized through mu, so the agent is safe for concurrent callers but does
// not multiplex: a second Ask blocks until the first returns.
type SubprocessAgent struct {
	command string
	args    []string
	env     []string // KEY=VALUE pairs passed to the subprocess

	mu     sync.Mutex
	proc   *subprocessState
	nextID int
}

// NewSubprocess creates a SubprocessAgent. The subprocess is not started until
// the first Ask call. command is the binary path; args are extra argv entries;
// env is the resolved environment map (KEY → value; already ${VAR}-substituted
// by the plugin loader).
func NewSubprocess(command string, args []string, env map[string]string) *SubprocessAgent {
	envPairs := make([]string, 0, len(env))
	for k, v := range env {
		envPairs = append(envPairs, k+"="+v)
	}
	return &SubprocessAgent{
		command: command,
		args:    args,
		env:     envPairs,
	}
}

// Ask sends an agent.ask JSON-RPC request to the subprocess and waits for the
// response. The subprocess is spawned on the first call; subsequent calls reuse
// it. On subprocess crash (broken pipe, EOF, or non-zero exit), returns
// *AskError{Kind: "plugin_crash"} and sets the agent to respawn on the next
// Ask.
func (o *SubprocessAgent) Ask(ctx context.Context, req AskRequest) (AskResponse, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Ensure the subprocess is running.
	if o.proc == nil {
		if err := o.spawn(); err != nil {
			return AskResponse{}, &AskError{
				Kind:       "plugin_crash",
				Underlying: err,
				Detail:     fmt.Sprintf("subprocess agent: spawn %q: %v", o.command, err),
			}
		}
	}

	// Serialize request.
	params, err := json.Marshal(req)
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "plugin_crash",
			Underlying: err,
			Detail:     fmt.Sprintf("subprocess agent: marshal request: %v", err),
		}
	}

	o.nextID++
	rpcReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      o.nextID,
		Method:  "agent.ask",
		Params:  params,
	}
	reqBytes, err := json.Marshal(rpcReq)
	if err != nil {
		return AskResponse{}, &AskError{
			Kind:       "plugin_crash",
			Underlying: err,
			Detail:     fmt.Sprintf("subprocess agent: marshal rpc frame: %v", err),
		}
	}
	reqBytes = append(reqBytes, '\n')

	// Write request.
	if _, err := o.proc.stdin.Write(reqBytes); err != nil {
		o.killProc()
		return AskResponse{}, &AskError{
			Kind:       "plugin_crash",
			Underlying: err,
			Detail:     fmt.Sprintf("subprocess agent: write to stdin: %v", err),
		}
	}

	// Read response with context cancellation support.
	type readResult struct {
		line []byte
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := o.proc.reader.ReadBytes('\n')
		ch <- readResult{line, err}
	}()

	var rr readResult
	select {
	case <-ctx.Done():
		// Context cancelled — kill the subprocess so the reader goroutine unblocks.
		o.killProc()
		<-ch // drain goroutine
		return AskResponse{}, &AskError{
			Kind:       "deadline_exceeded",
			Underlying: ctx.Err(),
			Detail:     fmt.Sprintf("subprocess agent: context cancelled waiting for response: %v", ctx.Err()),
		}
	case rr = <-ch:
	}

	if rr.err != nil {
		partial := bytes.TrimRight(rr.line, "\n")
		o.killProc()
		detail := fmt.Sprintf("subprocess agent: read response: %v", rr.err)
		if len(partial) > 0 {
			detail = fmt.Sprintf("%s (partial bytes: %q)", detail, truncateBytes(partial, ErrorDetailTruncateBytes))
		}
		return AskResponse{}, &AskError{
			Kind:       "plugin_crash",
			Underlying: rr.err,
			Detail:     detail,
		}
	}

	// Parse JSON-RPC response.
	var resp jsonrpcResponse
	if err := json.Unmarshal(bytes.TrimRight(rr.line, "\n"), &resp); err != nil {
		o.killProc()
		return AskResponse{}, &AskError{
			Kind:       "plugin_crash",
			Underlying: err,
			Detail:     fmt.Sprintf("subprocess agent: unmarshal response frame: %v (raw: %q)", err, truncateBytes(rr.line, ErrorDetailTruncateBytes)),
		}
	}

	if resp.Error != nil {
		return AskResponse{}, &AskError{
			Kind:   "plugin_crash",
			Detail: fmt.Sprintf("subprocess agent: rpc error %d: %s", resp.Error.Code, resp.Error.Message),
		}
	}

	var askResp AskResponse
	if err := json.Unmarshal(resp.Result, &askResp); err != nil {
		return AskResponse{}, &AskError{
			Kind:       "plugin_crash",
			Underlying: err,
			Detail:     fmt.Sprintf("subprocess agent: unmarshal AskResponse: %v", err),
		}
	}

	return askResp, nil
}

// Close sends SIGTERM to the subprocess. If it does not exit within
// SubprocessTerminateTimeout, SIGKILL is sent. Close is idempotent; calling it
// on an unstarted agent is a no-op.
func (o *SubprocessAgent) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.proc == nil {
		return nil
	}
	return o.terminateProc()
}

// spawn starts the subprocess and wires up stdin/stdout. Called with mu held.
func (o *SubprocessAgent) spawn() error {
	cmd := exec.Command(o.command, o.args...)
	if len(o.env) > 0 {
		cmd.Env = append(cmd.Environ(), o.env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start: %w", err)
	}

	o.proc = &subprocessState{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}
	return nil
}

// killProc kills the subprocess immediately and clears the process state.
// Called with mu held.
func (o *SubprocessAgent) killProc() {
	if o.proc == nil {
		return
	}
	_ = o.proc.stdin.Close()
	_ = o.proc.cmd.Process.Kill()
	_ = o.proc.cmd.Wait()
	o.proc = nil
}

// terminateProc sends SIGTERM, waits SubprocessTerminateTimeout, then SIGKILL.
// Called with mu held.
func (o *SubprocessAgent) terminateProc() error {
	if o.proc == nil {
		return nil
	}
	p := o.proc
	o.proc = nil

	_ = p.stdin.Close()

	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		// Process may already be dead.
		_ = p.cmd.Wait()
		return nil
	}

	done := make(chan struct{})
	go func() {
		_ = p.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(SubprocessTerminateTimeout):
		_ = p.cmd.Process.Signal(syscall.SIGKILL)
		<-done
	}
	return nil
}

// truncateBytes returns at most n bytes of b, appending "..." if truncated.
func truncateBytes(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return append(b[:n:n], []byte("...")...)
}
