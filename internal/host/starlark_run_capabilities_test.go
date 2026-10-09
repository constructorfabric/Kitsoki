package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

func writeHTTPStatusScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "status.star")
	if err := os.WriteFile(script, []byte(
		"def main(ctx):\n    resp = ctx.http.get(\"https://api.example.test/widget\")\n    return {\"status\": resp.status}\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script+".yaml", []byte(
		"outputs:\n  status: { type: int }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	return script
}

func cassetteRequiredHTTPCapabilities() map[string]any {
	return map[string]any{
		"http": map[string]any{
			"methods":           []any{"GET"},
			"cassette_required": true,
		},
	}
}

func TestStarlarkRun_CassetteRequiredRejectsImplicitProductionHTTP(t *testing.T) {
	res, err := StarlarkRunHandler(context.Background(), map[string]any{
		"script":       writeHTTPStatusScript(t),
		"capabilities": cassetteRequiredHTTPCapabilities(),
	})
	if err != nil {
		t.Fatalf("StarlarkRunHandler: %v", err)
	}
	if !strings.Contains(res.Error, "cassette_required") {
		t.Fatalf("Error = %q, want cassette_required rejection", res.Error)
	}
}

func TestStarlarkRun_CassetteRequiredAllowsInjectedReplayHTTP(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{
		Exchanges: []starlarkhost.HTTPEpisode{
			{
				Match: starlarkhost.HTTPMatch{
					Method: "GET",
					URL:    "https://api.example.test/widget",
				},
				Response: starlarkhost.HTTPCanned{Status: 204},
			},
		},
	}
	ctx := starlarkhost.WithHTTP(context.Background(), starlarkhost.NewReplayClient(cas))

	res, err := StarlarkRunHandler(ctx, map[string]any{
		"script":       writeHTTPStatusScript(t),
		"capabilities": cassetteRequiredHTTPCapabilities(),
	})
	if err != nil {
		t.Fatalf("StarlarkRunHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected domain error: %s", res.Error)
	}
	if got := res.Data["status"]; got != int64(204) {
		t.Fatalf("status = %v (%T), want int64(204)", got, got)
	}
	if got := res.Data[starlarkhost.ExchangesOutputKey]; got == nil {
		t.Fatalf("expected %s summary for replayed request", starlarkhost.ExchangesOutputKey)
	}
}
