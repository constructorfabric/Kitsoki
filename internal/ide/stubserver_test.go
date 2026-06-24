package ide

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// stubServer is an in-process MCP-over-ws editor stub: a real httptest.Server
// upgraded to a WebSocket via coder/websocket. It mirrors the verified wire
// contract — it rejects a bad auth token with close code 1008, answers the
// initialize / tools/list handshake, and answers tools/call for the host.ide.*
// tool set with canned results. It also writes a <port>.lock into a temp HOME's
// ~/.claude/ide so a Discoverer can find it. This is the backbone of every ide
// e2e test; keep it faithful and reusable.
type stubServer struct {
	*httptest.Server
	t         *testing.T
	authToken string
	port      int
	lockDir   string // the temp ~/.claude/ide this stub wrote its lock into

	mu      sync.Mutex
	calls   []string                  // tools/call names, in order (for assertions)
	results map[string]map[string]any // per-tool canned result override

	// hooks let a test inject faults.
	onConnect  func(authOK bool) // observe auth outcomes
	dropAfter  int               // if >0, drop the socket after N tools/call (0 = never)
	dropOnCall bool              // if true, close the socket on a tools/call WITHOUT replying (fails the call in-flight)
	callCount  int
}

// newStubServer starts a stub editor and writes its lock file into a temp
// ~/.claude/ide under t.TempDir(). The returned lockDir is what a Discoverer's
// LockDir should point at; auth token is fresh per stub.
func newStubServer(t *testing.T) *stubServer {
	t.Helper()
	s := &stubServer{
		t:         t,
		authToken: "stub-token-" + t.Name(),
		results:   map[string]map[string]any{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Server.Close)

	// Derive the port the dialer will use from the httptest listener address.
	addr := strings.TrimPrefix(s.Server.URL, "http://")
	_, portStr, err := splitHostPort(addr)
	if err != nil {
		t.Fatalf("parse stub addr %q: %v", addr, err)
	}
	s.port, _ = strconv.Atoi(portStr)

	s.lockDir = filepath.Join(t.TempDir(), ".claude", "ide")
	if err := os.MkdirAll(s.lockDir, 0o700); err != nil {
		t.Fatalf("mkdir lockdir: %v", err)
	}
	return s
}

// splitHostPort splits "host:port"; a tiny local helper to avoid importing net
// just for this in the test file's import block sanity.
func splitHostPort(addr string) (host, port string, err error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", nil
	}
	return addr[:i], addr[i+1:], nil
}

// writeLock writes a <port>.lock into the stub's lockDir for the given
// workspace folders. It uses the stub's real listener port so a Dial against
// the discovered lock actually reaches this server. Returns the parsed Lock.
func (s *stubServer) writeLock(workspaces ...string) Lock {
	s.t.Helper()
	lk := Lock{
		Port:             s.port,
		PID:              4242,
		WorkspaceFolders: workspaces,
		IDEName:          "Stub Code",
		Transport:        "ws",
		AuthToken:        s.authToken,
	}
	body, _ := json.Marshal(struct {
		PID              int      `json:"pid"`
		WorkspaceFolders []string `json:"workspaceFolders"`
		IDEName          string   `json:"ideName"`
		Transport        string   `json:"transport"`
		RunningInWindows bool     `json:"runningInWindows"`
		AuthToken        string   `json:"authToken"`
	}{lk.PID, lk.WorkspaceFolders, lk.IDEName, lk.Transport, false, lk.AuthToken})
	path := filepath.Join(s.lockDir, strconv.Itoa(s.port)+".lock")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		s.t.Fatalf("write lock: %v", err)
	}
	lk.Path = path
	return lk
}

// removeLock deletes the stub's own <port>.lock from its lockDir (used to make
// room for a replacement editor's lock at reconnect time).
func removeLock(s *stubServer) error {
	return os.Remove(filepath.Join(s.lockDir, strconv.Itoa(s.port)+".lock"))
}

// writeLockInto writes target's lock (port + token pointing at target) into an
// arbitrary lockDir — used to simulate discovery finding a freshest lock for a
// different live server than the one that wrote it.
func writeLockInto(t *testing.T, dir string, target *stubServer, workspaces ...string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"pid":              4242,
		"workspaceFolders": workspaces,
		"ideName":          "Stub Code",
		"transport":        "ws",
		"runningInWindows": false,
		"authToken":        target.authToken,
	})
	path := filepath.Join(dir, strconv.Itoa(target.port)+".lock")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write lock into %s: %v", dir, err)
	}
}

// lock returns the Lock that points directly at this stub (no discovery).
func (s *stubServer) lock(workspaces ...string) Lock {
	return Lock{
		Port:             s.port,
		IDEName:          "Stub Code",
		Transport:        "ws",
		AuthToken:        s.authToken,
		WorkspaceFolders: workspaces,
	}
}

// discoverer returns a Discoverer scoped to this stub's lockDir with a
// controllable env reader (default: empty, i.e. no CLAUDE_CODE_SSE_PORT).
func (s *stubServer) discoverer(env map[string]string) *Discoverer {
	return &Discoverer{
		LockDir: s.lockDir,
		Environ: func(k string) string { return env[k] },
	}
}

// gotCalls returns the recorded tools/call names so far.
func (s *stubServer) gotCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *stubServer) handle(w http.ResponseWriter, r *http.Request) {
	authOK := r.Header.Get(authHeader) == s.authToken
	if s.onConnect != nil {
		s.onConnect(authOK)
	}
	if !authOK {
		// Mirror the extension: close the upgraded socket with 1008.
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.Close(websocket.StatusPolicyViolation, "Unauthorized")
		return
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")

	ctx := context.Background()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return
		}
		var f rpcFrame
		if err := json.Unmarshal(data, &f); err != nil {
			continue
		}
		if f.Method == "" || f.ID == nil {
			continue // notification (e.g. notifications/initialized): no reply
		}
		// In-flight drop: on a tools/call, close the socket WITHOUT replying so
		// the client's pending waiter is failed by the read-pump drain (not left
		// hanging to ctx timeout). The handshake methods still get answered.
		if s.dropOnCall && f.Method == "tools/call" {
			c.Close(websocket.StatusInternalError, "drop-in-flight")
			return
		}
		resp := s.respond(f.Method, *f.ID, data)
		if resp == nil {
			continue
		}
		b, _ := json.Marshal(resp)
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			return
		}
		// Optional fault injection: drop the socket after N tools/call.
		if f.Method == "tools/call" {
			s.mu.Lock()
			s.callCount++
			drop := s.dropAfter > 0 && s.callCount >= s.dropAfter
			s.mu.Unlock()
			if drop {
				c.Close(websocket.StatusInternalError, "drop")
				return
			}
		}
	}
}

// respond builds the canned response for one request frame.
func (s *stubServer) respond(method string, id int, raw []byte) *rpcResponse {
	mkResult := func(v any) *rpcResponse {
		b, _ := json.Marshal(v)
		return &rpcResponse{JSONRPC: "2.0", ID: id, Result: b}
	}
	switch method {
	case "initialize":
		return mkResult(map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "stub-vscode", "version": "0.0.1"},
		})
	case "tools/list":
		return mkResult(map[string]any{"tools": stubTools()})
	case "tools/call":
		var req struct {
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		_ = json.Unmarshal(raw, &req)
		name := req.Params.Name
		s.mu.Lock()
		s.calls = append(s.calls, name)
		override := s.results[name]
		s.mu.Unlock()
		return mkResult(s.toolResult(name, override))
	default:
		return &rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32601, Message: "method not found"}}
	}
}

// toolResult returns the MCP result envelope for a tool, with the payload
// JSON-encoded into content[0].text (mirroring how the real extension wraps
// structured results). An override replaces the canned payload.
func (s *stubServer) toolResult(name string, override map[string]any) map[string]any {
	var payload map[string]any
	if override != nil {
		payload = override
	} else {
		payload = cannedToolPayload(name)
	}
	text, _ := json.Marshal(payload)
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
		"isError": false,
	}
}

// cannedToolPayload returns the default structured result for each host.ide.*
// tool.
//
// FIDELITY (this is load-bearing — a divergence here is what let /ide ship
// broken): these are the shapes a REAL editor (VS Code / Cursor / Windsurf over
// the Claude Code IDE MCP) actually returns, NOT a convenient invention. The
// earlier stub returned {file,…}/{editors:[{file,active}]} — shapes nothing real
// produces — so the handlers and the stub agreed by sharing the same wrong guess
// and the e2e proved nothing. The shapes below were captured from a live editor
// (getOpenEditors: TabGroups `tabs` with `fileName`/`uri`/`isActive`;
// getCurrentSelection: `filePath` + a `selection` with {line,character}). The
// host handlers must NORMALISE these to {file,text,range}/{editors}; the e2e
// asserts the normalised result, so it now tests real parsing. The `ide_live`
// build-tagged test re-captures these from a running editor and flags drift.
func cannedToolPayload(name string) map[string]any {
	switch name {
	case "getDiagnostics":
		return map[string]any{"uri": "file:///abs/file.go", "diagnostics": []map[string]any{{
			"message": "undefined: x", "severity": "error", "source": "compiler",
			"range": map[string]any{
				"start": map[string]any{"line": 4, "character": 2},
				"end":   map[string]any{"line": 4, "character": 8},
			},
		}}}
	case "getCurrentSelection":
		return map[string]any{
			"filePath": "/abs/file.go",
			"text":     "selected text",
			"selection": map[string]any{
				"start": map[string]any{"line": 1, "character": 0},
				"end":   map[string]any{"line": 2, "character": 3},
			},
		}
	case "getOpenEditors":
		return map[string]any{"tabs": []map[string]any{{
			"uri": "file:///abs/file.go", "fileName": "/abs/file.go",
			"isActive": true, "isGroupActive": true, "isDirty": false,
			"label": "file.go", "languageId": "go",
		}}}
	case "openFile":
		return map[string]any{"ok": true}
	case "openDiff":
		return map[string]any{"ok": true}
	default:
		return map[string]any{}
	}
}

// stubTools is the tools/list payload mirroring the verified tool names.
func stubTools() []map[string]any {
	names := []string{"getDiagnostics", "getCurrentSelection", "getOpenEditors", "openFile", "openDiff"}
	out := make([]map[string]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{
			"name":        n,
			"description": n + " (stub)",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		})
	}
	return out
}
