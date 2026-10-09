package starlark_test

import (
	"context"
	"strings"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

// fakeHTTP is an in-memory HTTPClient used to exercise the ctx.http surface
// without any network. It records the requests it saw and returns a canned
// response keyed by method+url.
type fakeHTTP struct {
	calls     []fakeCall
	responses map[string]*starlarkhost.HTTPResponse
}

type fakeCall struct {
	method  string
	url     string
	headers map[string]string
	body    []byte
}

func httpCaps() starlarkhost.CapabilitySpec {
	return starlarkhost.CapabilitySpec{
		World: true,
		HTTP:  starlarkhost.HTTPCapability{Enabled: true},
	}
}

func (f *fakeHTTP) Do(_ context.Context, method, url string, headers map[string]string, body []byte) (*starlarkhost.HTTPResponse, error) {
	f.calls = append(f.calls, fakeCall{method: method, url: url, headers: headers, body: body})
	if r, ok := f.responses[method+" "+url]; ok {
		return r, nil
	}
	// Default: a 200 echoing nothing — keeps the fake simple for callers that
	// only care that the request went through.
	return &starlarkhost.HTTPResponse{Status: 200, Headers: map[string]string{}, Body: []byte("{}")}, nil
}

// TestRun_TrivialScript runs a script that touches no I/O and returns a value.
func TestRun_TrivialScript(t *testing.T) {
	res, err := starlarkhost.Run(context.Background(), starlarkhost.Params{
		Script: "trivial.star",
		Source: []byte("def main(ctx):\n    return {\"x\": 1 + 2}\n"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.Outputs["x"]; got != int64(3) {
		t.Fatalf("x = %v (%T), want int64(3)", got, got)
	}
}

func TestRun_YAMLDecode(t *testing.T) {
	script := `
def main(ctx):
    doc = yaml.decode("items:\n  - id: one\n    count: 2\n")
    return {"id": doc["items"][0]["id"], "count": doc["items"][0]["count"]}
`
	res, err := starlarkhost.Run(context.Background(), starlarkhost.Params{Script: "yaml.star", Source: []byte(script)})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.Outputs["id"]; got != "one" {
		t.Fatalf("id = %v, want one", got)
	}
	if got := res.Outputs["count"]; got != int64(2) {
		t.Fatalf("count = %v (%T), want int64(2)", got, got)
	}
}

// TestRun_HTTPGet_ThroughFakeClient confirms ctx.http.get routes through the
// injected HTTPClient (a fake, no network) and that .status / .json() work.
func TestRun_HTTPGet_ThroughFakeClient(t *testing.T) {
	fake := &fakeHTTP{responses: map[string]*starlarkhost.HTTPResponse{
		"GET https://api.example.com/widget/7": {
			Status:  200,
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    []byte(`{"name":"gear","qty":3}`),
		},
	}}
	ctx := starlarkhost.WithHTTP(context.Background(), fake)

	script := `
def main(ctx):
    resp = ctx.http.get("https://api.example.com/widget/7", headers={"Accept": "application/json"})
    body = resp.json()
    return {"status": resp.status, "name": body["name"], "qty": body["qty"]}
`
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{Script: "get.star", Source: []byte(script), Capabilities: httpCaps()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outputs["status"] != int64(200) {
		t.Fatalf("status = %v, want 200", res.Outputs["status"])
	}
	if res.Outputs["name"] != "gear" {
		t.Fatalf("name = %v, want gear", res.Outputs["name"])
	}
	// JSON numbers decode as float64, so resp.json()["qty"] round-trips to
	// float64(3) — the int/number sidecar type accepts either.
	if res.Outputs["qty"] != float64(3) {
		t.Fatalf("qty = %v (%T), want float64(3)", res.Outputs["qty"], res.Outputs["qty"])
	}
	// The fake saw exactly one GET with the Accept header.
	if len(fake.calls) != 1 {
		t.Fatalf("fake.calls = %d, want 1", len(fake.calls))
	}
	c := fake.calls[0]
	if c.method != "GET" || c.url != "https://api.example.com/widget/7" {
		t.Fatalf("call = %s %s, want GET .../widget/7", c.method, c.url)
	}
	if c.headers["Accept"] != "application/json" {
		t.Fatalf("Accept header = %q, want application/json", c.headers["Accept"])
	}
	// Exchange summaries are surfaced only for the recording/replay clients
	// (see TestRun_HTTPReplay in smoke_test.go and the flow fixtures); a bare
	// fake client is neither, so Exchanges stays empty here by design.
}

// TestRun_HTTPPost_ThroughFakeClient confirms ctx.http.post sends a JSON body
// (dict → application/json) and that .text() returns the raw body.
func TestRun_HTTPPost_ThroughFakeClient(t *testing.T) {
	fake := &fakeHTTP{responses: map[string]*starlarkhost.HTTPResponse{
		"POST https://api.example.com/widgets": {
			Status:  201,
			Headers: map[string]string{},
			Body:    []byte("created"),
		},
	}}
	ctx := starlarkhost.WithHTTP(context.Background(), fake)

	script := `
def main(ctx):
    resp = ctx.http.post("https://api.example.com/widgets", body={"name": "bolt"})
    return {"status": resp.status, "text": resp.text()}
`
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{Script: "post.star", Source: []byte(script), Capabilities: httpCaps()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outputs["status"] != int64(201) {
		t.Fatalf("status = %v, want 201", res.Outputs["status"])
	}
	if res.Outputs["text"] != "created" {
		t.Fatalf("text = %v, want created", res.Outputs["text"])
	}
	if len(fake.calls) != 1 {
		t.Fatalf("fake.calls = %d, want 1", len(fake.calls))
	}
	c := fake.calls[0]
	if c.method != "POST" {
		t.Fatalf("method = %s, want POST", c.method)
	}
	if c.headers["Content-Type"] != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json (defaulted for dict body)", c.headers["Content-Type"])
	}
	if !strings.Contains(string(c.body), `"name":"bolt"`) {
		t.Fatalf("body = %q, want JSON-encoded {name: bolt}", string(c.body))
	}
}

// TestRun_Non2xxNotError confirms a non-2xx status reaches the script as a
// response (truthiness false) rather than aborting the run — only a transport
// error aborts.
func TestRun_Non2xxNotError(t *testing.T) {
	fake := &fakeHTTP{responses: map[string]*starlarkhost.HTTPResponse{
		"GET https://api.example.com/missing": {Status: 404, Headers: map[string]string{}, Body: []byte("nope")},
	}}
	ctx := starlarkhost.WithHTTP(context.Background(), fake)

	script := `
def main(ctx):
    resp = ctx.http.get("https://api.example.com/missing")
    ok = True if resp else False
    return {"status": resp.status, "ok": ok}
`
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{Script: "miss.star", Source: []byte(script), Capabilities: httpCaps()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outputs["status"] != int64(404) {
		t.Fatalf("status = %v, want 404", res.Outputs["status"])
	}
	if res.Outputs["ok"] != false {
		t.Fatalf("ok = %v, want false (404 is falsy)", res.Outputs["ok"])
	}
}

func TestRun_HTTPWithoutCapability_HasNoCtxAttr(t *testing.T) {
	script := `
def main(ctx):
    resp = ctx.http.get("https://api.example.com/x")
    return {"status": resp.status}
`
	_, err := starlarkhost.Run(context.Background(), starlarkhost.Params{Script: "denied.star", Source: []byte(script)})
	if err == nil {
		t.Fatal("expected error from absent ctx.http")
	}
	msg, ok := starlarkhost.AsDomainError(err)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, ".http") {
		t.Fatalf("error %q should mention missing ctx.http", msg)
	}
}

// TestRun_NoHTTPClient_Denied confirms that when http is granted but no client
// is injected, the capability fails loudly rather than silently reaching the
// network.
func TestRun_NoHTTPClient_Denied(t *testing.T) {
	script := `
def main(ctx):
    resp = ctx.http.get("https://api.example.com/x")
    return {"status": resp.status}
`
	_, err := starlarkhost.Run(context.Background(), starlarkhost.Params{Script: "denied.star", Source: []byte(script), Capabilities: httpCaps()})
	if err == nil {
		t.Fatal("expected error from deny-all HTTP client")
	}
	msg, ok := starlarkhost.AsDomainError(err)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, "no HTTP client configured") {
		t.Fatalf("error %q should mention no HTTP client configured", msg)
	}
}

// TestRun_UnknownCtxAttr fails with a clear "has no .env field" traceback — the
// narrow-ctx safety net (ctx exposes only inputs/world/http/fs/probe; anything
// else, like an env or arbitrary-subprocess surface, simply does not exist).
func TestRun_UnknownCtxAttr(t *testing.T) {
	script := `
def main(ctx):
    return {"data": ctx.env.get("SECRET")}
`
	_, err := starlarkhost.Run(context.Background(), starlarkhost.Params{Script: "evil.star", Source: []byte(script)})
	if err == nil {
		t.Fatal("expected error for unknown ctx attribute")
	}
	msg, ok := starlarkhost.AsDomainError(err)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, "env") || !strings.Contains(strings.ToLower(msg), "has no") {
		t.Fatalf("error %q should report ctx has no .env field", msg)
	}
}

// TestRun_WorldGet_AbsentReturnsNone confirms ctx.world.get returns None for an
// absent key and the value for a present one.
func TestRun_WorldGet_AbsentReturnsNone(t *testing.T) {
	script := `
def main(ctx):
    present = ctx.world.get("here")
    absent = ctx.world.get("nope")
    return {"present": present, "absent_is_none": absent == None}
`
	res, err := starlarkhost.Run(context.Background(), starlarkhost.Params{
		Script: "world.star",
		Source: []byte(script),
		World:  map[string]any{"here": "value"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Outputs["present"] != "value" {
		t.Fatalf("present = %v, want value", res.Outputs["present"])
	}
	if res.Outputs["absent_is_none"] != true {
		t.Fatalf("absent_is_none = %v, want true", res.Outputs["absent_is_none"])
	}
}

// TestRun_NormalizesExoticNumericKinds locks that inputs and world values
// arriving as non-canonical Go integer kinds (uint64/int32/... — effect
// templating and expression evaluation both produce these, e.g. a world
// counter incremented by an expr) are normalized to int64 before sidecar
// validation and ctx conversion. Without the normalization, a uint64 input
// declared `int` fails validateInputs ("expected int, got uint64" — hit live
// by scenario-qa's judge room passing leg_index) and a uint64 world value
// errors goToStarlark's strict type switch on ctx.world.get.
func TestRun_NormalizesExoticNumericKinds(t *testing.T) {
	script := `
def main(ctx):
    return {"total": ctx.inputs["leg_index"] + ctx.world.get("count")}
`
	res, err := starlarkhost.Run(context.Background(), starlarkhost.Params{
		Script: "numeric.star",
		Source: []byte(script),
		Sidecar: &starlarkhost.Sidecar{
			Inputs:  map[string]starlarkhost.FieldSpec{"leg_index": {Type: "int"}},
			Outputs: map[string]starlarkhost.FieldSpec{"total": {Type: "int"}},
		},
		Inputs: map[string]any{"leg_index": uint64(2)},
		World:  map[string]any{"count": int32(3)},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.Outputs["total"]; got != int64(5) {
		t.Fatalf("total = %v (%T), want int64(5)", got, got)
	}
}

// TestRun_ConvertsGoNativeSliceAndMapTypes locks that goToStarlark accepts
// concretely-typed Go slices/maps ([]string, []map[string]any, …), not just
// the []any/map[string]any shapes a JSON decode produces. Go-native host
// handlers (agent stubs, wrapped CLI output, structured records) commonly
// build inputs by hand with these concrete types; hit live by a bugfix
// dogfood smoke test whose stubbed agent artifact carried
// affected_files []string and involved_components []map[string]any straight
// into host.starlark.run's inputs.
func TestRun_ConvertsGoNativeSliceAndMapTypes(t *testing.T) {
	script := `
def main(ctx):
    return {
        "files": ctx.inputs["files"],
        "first_component": ctx.inputs["components"][0]["name"],
    }
`
	res, err := starlarkhost.Run(context.Background(), starlarkhost.Params{
		Script: "native_types.star",
		Source: []byte(script),
		Inputs: map[string]any{
			"files":      []string{"a.go", "b.go"},
			"components": []map[string]any{{"name": "internal/orchestrator"}},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	files, ok := res.Outputs["files"].([]any)
	if !ok || len(files) != 2 || files[0] != "a.go" || files[1] != "b.go" {
		t.Fatalf("files = %#v, want [a.go b.go]", res.Outputs["files"])
	}
	if got := res.Outputs["first_component"]; got != "internal/orchestrator" {
		t.Fatalf("first_component = %v, want internal/orchestrator", got)
	}
}
