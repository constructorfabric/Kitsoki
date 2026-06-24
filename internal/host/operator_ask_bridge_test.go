package host

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kitsokimcp "kitsoki/internal/mcp"
)

// captureSlog installs a text-handler slog default backed by a buffer for the
// duration of the test and restores the previous default. Returns the buffer so
// the caller can assert on emitted structured attributes.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// dialAsk sends one request to the listener and reads one response, mirroring
// what the mcp-operator-ask grandchild does on the wire.
func dialAsk(t *testing.T, dial func() (net.Conn, error), req kitsokimcp.OperatorAskRequest) kitsokimcp.OperatorAskResponse {
	t.Helper()
	conn, err := dial()
	require.NoError(t, err)
	defer conn.Close()
	payload, _ := json.Marshal(req)
	_, err = conn.Write(append(payload, '\n'))
	require.NoError(t, err)
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	require.NoError(t, err)
	var resp kitsokimcp.OperatorAskResponse
	require.NoError(t, json.Unmarshal(line, &resp))
	return resp
}

func TestOperatorAskListener_UsesSocketSafeTempDir(t *testing.T) {
	t.Setenv("KITSOKI_OPERATOR_ASK_SOCKET_DIR", "")
	l, err := startOperatorAskListener(context.Background(), &fakePrompter{}, "s", time.Minute)
	if err != nil {
		t.Skipf("unix socket bind unavailable in this sandbox: %v", err)
	}
	defer l.close()

	found := false
	for _, dir := range operatorAskSocketDirs() {
		if strings.HasPrefix(l.sockPath, dir+string(os.PathSeparator)) {
			found = true
			break
		}
	}
	assert.True(t, found, "socket path should use a configured candidate dir: %s", l.sockPath)
}

func TestOperatorAskListener_RespectsSocketDirOverride(t *testing.T) {
	// A SHORT base dir keeps the unix socket path under the sun_path limit
	// (t.TempDir()/os.TempDir() can be too long on macOS → bind fails). Prefer
	// macOS's /private/tmp, falling back to /tmp on Linux CI where the former
	// does not exist (otherwise MkdirTemp errors and the test hard-fails).
	base := "/private/tmp"
	if _, statErr := os.Stat(base); statErr != nil {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "ks-opask")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("KITSOKI_OPERATOR_ASK_SOCKET_DIR", dir)

	l, err := startOperatorAskListener(context.Background(), &fakePrompter{}, "s", time.Minute)
	if err != nil {
		t.Skipf("unix socket bind unavailable in this sandbox: %v", err)
	}
	defer l.close()
	assert.True(t, strings.HasPrefix(l.sockPath, dir+string(os.PathSeparator)), "socket path should use override dir: %s", l.sockPath)
}

func TestOperatorAskListener_RoundTrip(t *testing.T) {
	logBuf := captureSlog(t)
	fake := &fakePrompter{answers: map[string]any{"Which env?": "prod"}}
	l, err := StartOperatorAskListenerInMemoryForTest(context.Background(), fake, "sess-9", time.Minute)
	require.NoError(t, err)
	defer l.Close()

	resp := dialAsk(t, l.Dial, kitsokimcp.OperatorAskRequest{
		Questions: []kitsokimcp.OperatorAskQuestion{{
			Question:    "Which env?",
			Header:      "Env",
			Options:     []kitsokimcp.OperatorAskOption{{Label: "prod"}, {Label: "staging"}},
			MultiSelect: false,
		}},
	})
	require.Empty(t, resp.Error)
	assert.Equal(t, "prod", resp.Answers["Which env?"])

	// The prompter saw the mapped host-facing question + session id.
	assert.Equal(t, "sess-9", fake.gotSession)
	require.Len(t, fake.gotQuestions, 1)
	assert.Equal(t, "Env", fake.gotQuestions[0].Header)
	require.Len(t, fake.gotQuestions[0].Options, 2)
	assert.Equal(t, "prod", fake.gotQuestions[0].Options[0].Label)

	// The trace carries the correlatable structured attrs on the asked/answered
	// pair. Allow a brief settle since handleConn logs on a separate goroutine.
	require.Eventually(t, func() bool {
		return bytes.Contains(logBuf.Bytes(), []byte("operator.question.answered"))
	}, time.Second, 10*time.Millisecond)
	logs := logBuf.String()
	assert.Contains(t, logs, "operator.question.asked")
	assert.Contains(t, logs, "question_id=")
	assert.Contains(t, logs, "duration_ms=")
	assert.Contains(t, logs, "outcome=answered")
	assert.Contains(t, logs, "headers=")
	assert.Contains(t, logs, "Env")
}

func TestOperatorAskListener_PrompterErrorBecomesErrorFrame(t *testing.T) {
	logBuf := captureSlog(t)
	fake := &fakePrompter{err: errors.New("operator cancelled")}
	l, err := StartOperatorAskListenerInMemoryForTest(context.Background(), fake, "s", time.Minute)
	require.NoError(t, err)
	defer l.Close()

	resp := dialAsk(t, l.Dial, kitsokimcp.OperatorAskRequest{
		Questions: []kitsokimcp.OperatorAskQuestion{{Question: "q", Options: []kitsokimcp.OperatorAskOption{{Label: "a"}}}},
	})
	assert.Equal(t, "operator cancelled", resp.Error)
	assert.Nil(t, resp.Answers)

	// The unanswered terminal event carries the correlatable structured attrs.
	require.Eventually(t, func() bool {
		return bytes.Contains(logBuf.Bytes(), []byte("operator.question.unanswered"))
	}, time.Second, 10*time.Millisecond)
	logs := logBuf.String()
	assert.Contains(t, logs, "question_id=")
	assert.Contains(t, logs, "duration_ms=")
	assert.Contains(t, logs, "outcome=unanswered")
}

func TestOperatorAskListener_CloseRemovesSocket(t *testing.T) {
	l, err := startOperatorAskListener(context.Background(), &fakePrompter{}, "s", time.Minute)
	if err != nil {
		t.Skipf("unix socket bind unavailable in this sandbox: %v", err)
	}
	_, statErr := os.Stat(l.sockPath)
	require.NoError(t, statErr, "socket should exist while listening")
	l.close()
	_, statErr = os.Stat(l.sockPath)
	require.True(t, os.IsNotExist(statErr), "socket should be removed after close")
}

func TestOperatorAskListener_CtxCancelUnblocks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	l, err := StartOperatorAskListenerInMemoryForTest(ctx, &fakePrompter{}, "s", time.Minute)
	require.NoError(t, err)
	defer l.Close()
	cancel()
	// AfterFunc closes the listener; a subsequent dial should fail promptly.
	require.Eventually(t, func() bool {
		_, derr := l.Dial()
		return derr != nil
	}, time.Second, 10*time.Millisecond, "listener must stop accepting after ctx cancel")
}

func TestAttachOperatorAsk_NoopWhenNoOperator(t *testing.T) {
	ctx := context.Background()
	inArgs := []string{"-p", "--model", "x"}
	inTools := []string{"Read"}
	outArgs, outTools, cleanup, err := attachOperatorAsk(ctx, inArgs, inTools)
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	defer cleanup()
	assert.Equal(t, inArgs, outArgs, "args unchanged with no operator")
	assert.Equal(t, inTools, outTools, "tools unchanged with no operator")
	assert.NotContains(t, outArgs, "--mcp-config")
	assert.NotContains(t, outTools, operatorAskToolName)
}

func TestAttachOperatorAsk_WiresToolWhenInteractive(t *testing.T) {
	prevStarter := operatorAskListenerStarter
	operatorAskListenerStarter = func(ctx context.Context, prompter OperatorPrompter, sessionID string, timeout time.Duration) (*operatorAskListener, error) {
		ln := newPipeListener()
		if timeout <= 0 {
			timeout = operatorAskWaitTimeout
		}
		return startOperatorAskListenerOn(ctx, ln, "in-memory", prompter, sessionID, timeout), nil
	}
	t.Cleanup(func() { operatorAskListenerStarter = prevStarter })

	ctx := WithKitsokiSessionID(
		WithOperatorPrompter(context.Background(), &fakePrompter{}),
		"sess-42",
	)
	outArgs, outTools, cleanup, err := attachOperatorAsk(ctx, []string{"-p"}, []string{"Read"})
	require.NoError(t, err)
	defer cleanup()

	assert.Contains(t, outTools, operatorAskToolName, "the ask tool must be allowlisted")
	assert.Contains(t, outArgs, "--mcp-config")
	assert.Contains(t, outArgs, "--append-system-prompt")

	// The MCP config tempfile points at mcp-operator-ask with a --socket.
	cfgIdx := indexOfStr(outArgs, "--mcp-config")
	require.GreaterOrEqual(t, cfgIdx, 0)
	cfgPath := outArgs[cfgIdx+1]
	raw, readErr := os.ReadFile(cfgPath)
	require.NoError(t, readErr)
	assert.Contains(t, string(raw), operatorAskServerName)
	assert.Contains(t, string(raw), "mcp-operator-ask")
	assert.Contains(t, string(raw), "--socket")

	// The system clause names the tool.
	scIdx := indexOfStr(outArgs, "--append-system-prompt")
	require.GreaterOrEqual(t, scIdx, 0)
	assert.Contains(t, outArgs[scIdx+1], operatorAskToolName)

	// cleanup removes the config file.
	cleanup()
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr), "cleanup must remove the MCP config tempfile")
}

func indexOfStr(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
