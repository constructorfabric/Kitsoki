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
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{Script: "get.star", Source: []byte(script)})
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
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{Script: "post.star", Source: []byte(script)})
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
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{Script: "miss.star", Source: []byte(script)})
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

// TestRun_NoHTTPClient_Denied confirms that with no client injected, ctx.http
// fails loudly (deny-all) as a DomainError rather than silently reaching the
// network.
func TestRun_NoHTTPClient_Denied(t *testing.T) {
	script := `
def main(ctx):
    resp = ctx.http.get("https://api.example.com/x")
    return {"status": resp.status}
`
	_, err := starlarkhost.Run(context.Background(), starlarkhost.Params{Script: "denied.star", Source: []byte(script)})
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

// TestRun_UnknownCtxAttr fails with a clear "has no .fs field" traceback — the
// narrow-ctx safety net (no fs/env/subprocess surface exists).
func TestRun_UnknownCtxAttr(t *testing.T) {
	script := `
def main(ctx):
    return {"data": ctx.fs.read("secret")}
`
	_, err := starlarkhost.Run(context.Background(), starlarkhost.Params{Script: "evil.star", Source: []byte(script)})
	if err == nil {
		t.Fatal("expected error for unknown ctx attribute")
	}
	msg, ok := starlarkhost.AsDomainError(err)
	if !ok {
		t.Fatalf("expected DomainError, got %T: %v", err, err)
	}
	if !strings.Contains(msg, "fs") || !strings.Contains(strings.ToLower(msg), "has no") {
		t.Fatalf("error %q should report ctx has no .fs field", msg)
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
