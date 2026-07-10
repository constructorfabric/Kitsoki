package server_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	"kitsoki/internal/kit"
	"kitsoki/internal/kitendpoint"
	"kitsoki/internal/runstatus/server"
)

// syntheticKitDir is the S1 loader fixture kit (internal/app owns it), reused
// read-only here to exercise the kit.<kit>.<iface>.<op> JSON-RPC fallback and
// runstatus.kits.list end to end, with no LLM/network involved: the fixture's
// `reporter` interface defaults to host.run, which this test stubs out.
const syntheticKitDir = "../../app/testdata/kits/synthetic-kit"

func newKitDispatcher(t *testing.T) *kitendpoint.Dispatcher {
	t.Helper()
	def, err := kit.LoadDir(syntheticKitDir)
	require.NoError(t, err)
	kits := kit.NewRegistry()
	require.NoError(t, kits.Add(def))

	reg := host.NewRegistry()
	reg.Register("host.run", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"echo": args}}, nil
	})
	return kitendpoint.NewDispatcher(kits, reg)
}

func newKitServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(server.NewMulti(newStubProvider(), server.WithKits(newKitDispatcher(t))).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestKitDispatch_InvokesDeclaredInterfaceOp(t *testing.T) {
	ts := newKitServer(t)
	var out map[string]any
	rpcCall(t, ts, "kit.synthetic.reporter.announce", map[string]any{
		"message": "hello",
	}, &out)

	assert.Equal(t, true, out["ok"])
	data, ok := out["data"].(map[string]any)
	require.True(t, ok, "expected a data field, got %#v", out)
	echo, ok := data["echo"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "hello", echo["message"])
	// The interface's default binding is host.run; the op-suffix fallback
	// (host.run.announce -> host.run) must inject the dropped op as
	// args["op"], exactly like any other host_interface dispatch.
	assert.Equal(t, "announce", echo["op"])
}

func TestKitDispatch_UnknownKitErrors(t *testing.T) {
	ts := newKitServer(t)
	code, msg := rpcCallExpectError(t, ts, "kit.no-such-kit.reporter.announce", map[string]any{})
	assert.NotEqual(t, 0, code)
	assert.Contains(t, msg, "no-such-kit")
}

func TestKitDispatch_UndeclaredOpErrors(t *testing.T) {
	ts := newKitServer(t)
	code, msg := rpcCallExpectError(t, ts, "kit.synthetic.reporter.no-such-op", map[string]any{})
	assert.NotEqual(t, 0, code)
	assert.Contains(t, msg, "no-such-op")
}

func TestKitDispatch_NoDispatcherReportsMethodMissing(t *testing.T) {
	ts := httptest.NewServer(server.NewMulti(newStubProvider()).Handler())
	t.Cleanup(ts.Close)
	code, _ := rpcCallExpectError(t, ts, "kit.synthetic.reporter.announce", map[string]any{})
	assert.NotEqual(t, 0, code)
}

func TestKitsList_ReturnsInstalledKits(t *testing.T) {
	ts := newKitServer(t)
	var out []server.KitHeader
	rpcCall(t, ts, "runstatus.kits.list", map[string]any{}, &out)

	require.Len(t, out, 1)
	assert.Equal(t, "synthetic", out[0].Kit)
	assert.Equal(t, "kitsoki-test", out[0].Namespace)
	assert.Contains(t, out[0].Provides, "greeter")
}

func TestKitsList_EmptyWhenNoDispatcherAttached(t *testing.T) {
	ts := httptest.NewServer(server.NewMulti(newStubProvider()).Handler())
	t.Cleanup(ts.Close)
	var out []server.KitHeader
	rpcCall(t, ts, "runstatus.kits.list", map[string]any{}, &out)
	assert.Empty(t, out)
}
