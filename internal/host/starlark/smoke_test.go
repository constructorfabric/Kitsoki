package starlark_test

import (
	"context"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

// TestRun_HTTPReplay sanity-checks the replay seam: the real Run path serves
// ctx.http through an injected ReplayClient, never touching the network, and
// surfaces a body-free exchange summary. The real flow-fixture suite (owned by
// the tests agent) replaces this; it exists only so the seam is exercised.
func TestRun_HTTPReplay(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{
		Kind: "http_cassette",
		Exchanges: []starlarkhost.HTTPEpisode{
			{
				Match:    starlarkhost.HTTPMatch{Method: "GET", URL: "https://api.example.com/widget/42"},
				Response: starlarkhost.HTTPCanned{Status: 200, Body: `{"name":"sprocket"}`},
			},
		},
	}
	ctx := starlarkhost.WithHTTP(context.Background(), starlarkhost.NewReplayClient(cas))

	sidecar, err := starlarkhost.ParseSidecar([]byte("outputs:\n  name: { type: string }\n"))
	if err != nil {
		t.Fatalf("ParseSidecar: %v", err)
	}

	script := []byte(`
def main(ctx):
    resp = ctx.http.get("https://api.example.com/widget/42")
    if resp.status != 200:
        fail("bad status")
    return {"name": resp.json()["name"]}
`)

	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:  "widget.star",
		Source:  script,
		Sidecar: sidecar,
		Capabilities: starlarkhost.CapabilitySpec{
			World: true,
			HTTP:  starlarkhost.HTTPCapability{Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.Outputs["name"]; got != "sprocket" {
		t.Fatalf("name = %v, want sprocket", got)
	}
	if len(res.Exchanges) != 1 || res.Exchanges[0].Status != 200 {
		t.Fatalf("exchanges = %+v, want one 200", res.Exchanges)
	}
}

// TestRun_DomainError confirms a missing required input is a *DomainError (so
// the adapter maps it to Result.Error / on_error:) rather than a Go error.
func TestRun_DomainError(t *testing.T) {
	sidecar, err := starlarkhost.ParseSidecar([]byte("inputs:\n  n: { type: int, required: true }\n"))
	if err != nil {
		t.Fatalf("ParseSidecar: %v", err)
	}
	_, runErr := starlarkhost.Run(context.Background(), starlarkhost.Params{
		Script:  "x.star",
		Source:  []byte("def main(ctx):\n    return {}\n"),
		Sidecar: sidecar,
	})
	if runErr == nil {
		t.Fatal("expected error for missing required input")
	}
	if _, ok := starlarkhost.AsDomainError(runErr); !ok {
		t.Fatalf("expected DomainError, got %T: %v", runErr, runErr)
	}
}
