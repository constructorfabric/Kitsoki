package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/machine"
	kitsokimcp "kitsoki/internal/mcp"
)

// fakeKitCaller is a minimal kitsokimcp.KitCaller stub. Real production
// wiring goes through cmd/kitsoki's mcpKitCaller adapter over
// internal/kitendpoint.Dispatcher; this package only depends on the narrow
// KitCaller interface (see server.go's doc comment on why), so its own tests
// stub that seam directly rather than pulling in kitendpoint/kit/host.
type fakeKitCaller struct {
	// wantErr, when non-nil, is returned as the resolution error (err) rather
	// than a domain (ok=false) result.
	wantErr error
	// domainErr, when non-empty, is returned as the domain error message
	// (ok=false, errMsg=domainErr).
	domainErr string
	gotKit    string
	gotIface  string
	gotOp     string
	gotArgs   map[string]any
}

func (f *fakeKitCaller) Call(_ context.Context, kit, iface, op string, args map[string]any) (bool, map[string]any, string, error) {
	f.gotKit, f.gotIface, f.gotOp, f.gotArgs = kit, iface, op, args
	if f.wantErr != nil {
		return false, nil, "", f.wantErr
	}
	if f.domainErr != "" {
		return false, nil, f.domainErr, nil
	}
	return true, map[string]any{"echo": args}, "", nil
}

func callKitCall(ctx context.Context, cs *mcpsdk.ClientSession, args kitsokimcp.KitCallArgs) (*mcpsdk.CallToolResult, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var argsMap map[string]any
	if err := json.Unmarshal(argsJSON, &argsMap); err != nil {
		return nil, err
	}
	return cs.CallTool(ctx, &mcpsdk.CallToolParams{
		Name:      "kit_call",
		Arguments: argsMap,
	})
}

func newKitCallServer(t *testing.T, kc kitsokimcp.KitCaller) *kitsokimcp.Server {
	t.Helper()
	def := loadCloakApp(t)
	s := openInMemoryStore(t)
	m, err := machine.New(def)
	require.NoError(t, err)
	if kc != nil {
		return kitsokimcp.NewServer(m, s, def, kitsokimcp.WithKits(kc))
	}
	return kitsokimcp.NewServer(m, s, def)
}

func TestKitCall_InvokesDispatcher(t *testing.T) {
	fake := &fakeKitCaller{}
	srv := newKitCallServer(t, fake)
	ctx := context.Background()
	cs := connectInProcess(ctx, t, srv)

	res, err := callKitCall(ctx, cs, kitsokimcp.KitCallArgs{
		Kit:   "synthetic",
		Iface: "reporter",
		Op:    "announce",
		Args:  map[string]any{"message": "hi"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "expected ok, got error: %v", contentText(res))

	var out kitsokimcp.KitCallResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	assert.True(t, out.OK)

	assert.Equal(t, "synthetic", fake.gotKit)
	assert.Equal(t, "reporter", fake.gotIface)
	assert.Equal(t, "announce", fake.gotOp)
	assert.Equal(t, "hi", fake.gotArgs["message"])
}

func TestKitCall_NoDispatcherReturnsError(t *testing.T) {
	srv := newKitCallServer(t, nil)
	ctx := context.Background()
	cs := connectInProcess(ctx, t, srv)

	res, err := callKitCall(ctx, cs, kitsokimcp.KitCallArgs{Kit: "synthetic", Iface: "reporter", Op: "announce"})
	require.NoError(t, err)
	assert.True(t, res.IsError)

	var out kitsokimcp.KitCallResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	assert.False(t, out.OK)
	assert.Contains(t, out.Error, "no kits installed")
}

func TestKitCall_DomainErrorSurfacesAsError(t *testing.T) {
	fake := &fakeKitCaller{domainErr: "no such operation"}
	srv := newKitCallServer(t, fake)
	ctx := context.Background()
	cs := connectInProcess(ctx, t, srv)

	res, err := callKitCall(ctx, cs, kitsokimcp.KitCallArgs{Kit: "synthetic", Iface: "reporter", Op: "bogus"})
	require.NoError(t, err)
	assert.True(t, res.IsError)

	var out kitsokimcp.KitCallResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	assert.False(t, out.OK)
	assert.Equal(t, "no such operation", out.Error)
}

func TestKitCall_MissingRequiredFieldErrors(t *testing.T) {
	fake := &fakeKitCaller{}
	srv := newKitCallServer(t, fake)
	ctx := context.Background()
	cs := connectInProcess(ctx, t, srv)

	res, err := callKitCall(ctx, cs, kitsokimcp.KitCallArgs{Kit: "synthetic"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Empty(t, fake.gotIface, "dispatcher should not be called when required fields are missing")
}
