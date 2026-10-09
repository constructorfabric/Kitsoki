package tuibridge

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// dialTest connects to the bridge and returns a func that reads accumulated
// output frames until the given substring appears (or the deadline expires).
func dialTest(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/pty"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func waitForOutput(t *testing.T, conn *websocket.Conn, want string, timeout time.Duration) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var buf bytes.Buffer
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read (buffered so far: %q): %v", buf.String(), err)
		}
		buf.Write(data)
		if strings.Contains(buf.String(), want) {
			return buf.String()
		}
	}
}

// TestServer_EchoesKeystrokes verifies the core pty<->websocket bridge: bytes
// written to the websocket reach the spawned process's stdin, and the
// process's stdout reaches the websocket, byte-for-byte (modulo the tty's own
// line echo).
func TestServer_EchoesKeystrokes(t *testing.T) {
	srv := New(Options{Command: []string{"/bin/cat"}})
	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	conn := dialTest(t, ts)
	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("hello-bridge\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitForOutput(t, conn, "hello-bridge", 5*time.Second)
}

// TestServer_Resize verifies the JSON resize control frame actually resizes
// the pty before the spawned process reads it, by racing a resize frame
// against a shell that reports its terminal size once a line of input
// arrives — websocket frame ordering on one connection guarantees the resize
// is applied before the triggering input is delivered.
func TestServer_Resize(t *testing.T) {
	srv := New(Options{Command: []string{"/bin/sh", "-c", "read _; stty size"}})
	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	conn := dialTest(t, ts)
	ctx := context.Background()
	if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("go\n")); err != nil {
		t.Fatalf("write trigger: %v", err)
	}
	waitForOutput(t, conn, "40 120", 5*time.Second)
}

func TestServer_DefaultTerminalEnvEnablesANSIFidelity(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("COLORTERM", "")
	t.Setenv("FORCE_COLOR", "0")
	t.Setenv("CLICOLOR_FORCE", "0")

	script := strings.Join([]string{
		`printf 'TERM=%s\n' "$TERM"`,
		`printf 'COLORTERM=%s\n' "$COLORTERM"`,
		`printf 'FORCE_COLOR=%s\n' "$FORCE_COLOR"`,
		`printf 'CLICOLOR_FORCE=%s\n' "$CLICOLOR_FORCE"`,
		`printf 'NO_COLOR=%s\n' "${NO_COLOR-unset}"`,
		`printf '\033[1;31mstyle-probe\033[0m\n'`,
	}, "; ")
	srv := New(Options{Command: []string{"/bin/sh", "-c", script}})
	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	conn := dialTest(t, ts)
	out := waitForOutput(t, conn, "style-probe", 5*time.Second)
	for _, want := range []string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"FORCE_COLOR=1",
		"CLICOLOR_FORCE=1",
		"NO_COLOR=unset",
		"\x1b[1;31mstyle-probe\x1b[0m",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestServer_ExplicitEnvOverridesTerminalDefaults(t *testing.T) {
	t.Setenv("TERM", "dumb")
	t.Setenv("NO_COLOR", "1")

	script := strings.Join([]string{
		`printf 'TERM=%s\n' "$TERM"`,
		`printf 'COLORTERM=%s\n' "$COLORTERM"`,
		`printf 'FORCE_COLOR=%s\n' "$FORCE_COLOR"`,
		`printf 'NO_COLOR=%s\n' "${NO_COLOR-unset}"`,
	}, "; ")
	srv := New(Options{
		Command: []string{"/bin/sh", "-c", script},
		Env:     []string{"TERM=ansi", "COLORTERM=8bit", "FORCE_COLOR=0", "NO_COLOR=1"},
	})
	ts := httptest.NewServer(http.HandlerFunc(srv.ServeHTTP))
	defer ts.Close()

	conn := dialTest(t, ts)
	out := waitForOutput(t, conn, "NO_COLOR=1", 5*time.Second)
	for _, want := range []string{
		"TERM=ansi",
		"COLORTERM=8bit",
		"FORCE_COLOR=0",
		"NO_COLOR=1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}
