package ticketprovider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
	"kitsoki/internal/ticketprovider"
)

type fakeHTTPClient struct {
	status  int
	body    string
	headers map[string]string
	called  bool
}

func (f *fakeHTTPClient) Do(_ context.Context, method, url string, headers map[string]string, _ []byte) (*starlarkhost.HTTPResponse, error) {
	f.called = true
	f.headers = map[string]string{}
	for k, v := range headers {
		f.headers[k] = v
	}
	status := f.status
	if status == 0 {
		status = 200
	}
	return &starlarkhost.HTTPResponse{
		Status:  status,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(f.body),
	}, nil
}

func writeProvider(t *testing.T, script, sidecar string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "provider.star")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".yaml", []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStarlarkProvider_AuthInjectedByTransport(t *testing.T) {
	script := `
def search(ctx):
    resp = ctx.http.get(ctx.inputs["base_url"] + "/tickets", auth = "bitbucket_pat")
    payload = resp.json()
    return {"tickets": payload["tickets"], "saw_secret_input": "BITBUCKET_PAT" in ctx.inputs}
`
	sidecar := `
kind: ticket_provider/v1
http:
  methods: [GET]
  hosts: [tickets.example.invalid]
auth:
  bitbucket_pat:
    env: BITBUCKET_PAT
    header: Authorization
    prefix: "Bearer "
    missing_code: missing_bitbucket_pat
`
	path := writeProvider(t, script, sidecar)
	fake := &fakeHTTPClient{body: `{"tickets":[{"id":"T-1","title":"One"}]}`}
	res, err := (&ticketprovider.StarlarkProvider{
		Script: path,
		HTTP:   fake,
		Env: func(context.Context, string) string {
			return "super-secret"
		},
	}).Invoke(context.Background(), "search", map[string]any{"base_url": "https://tickets.example.invalid"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.Error != nil {
		t.Fatalf("unexpected provider error: %v", res.Error)
	}
	if !fake.called {
		t.Fatal("expected fake HTTP client to be called")
	}
	if got := fake.headers["Authorization"]; got != "Bearer super-secret" {
		t.Fatalf("Authorization header = %q, want transport-injected bearer token", got)
	}
	if got := res.Data["saw_secret_input"]; got != false {
		t.Fatalf("script saw secret input = %v, want false", got)
	}
}

func TestStarlarkProvider_AuthRecordingClientReportsExchanges(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tickets":[{"id":"T-2","title":"Two"}]}`))
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	script := `
def search(ctx):
    resp = ctx.http.get(ctx.inputs["base_url"] + "/tickets", auth = "jira")
    return resp.json()
`
	sidecar := `
kind: ticket_provider/v1
http:
  methods: [GET]
  hosts: [` + u.Hostname() + `]
auth:
  jira:
    env: JIRA_TOKEN
    header: Authorization
    prefix: "Bearer "
`
	path := writeProvider(t, script, sidecar)
	res, err := (&ticketprovider.StarlarkProvider{
		Script: path,
		Env: func(context.Context, string) string {
			return "jira-secret"
		},
	}).Invoke(context.Background(), "search", map[string]any{"base_url": srv.URL})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.Error != nil {
		t.Fatalf("unexpected provider error: %v", res.Error)
	}
	if sawAuth != "Bearer jira-secret" {
		t.Fatalf("Authorization header = %q, want transport-injected token", sawAuth)
	}
	if len(res.Exchanges) != 1 {
		t.Fatalf("exchanges = %#v, want one exchange", res.Exchanges)
	}
	if res.Exchanges[0].Status != 200 {
		t.Fatalf("exchange status = %d, want 200", res.Exchanges[0].Status)
	}
}

func TestStarlarkProvider_MissingCredentialReturnsCustomAuthError(t *testing.T) {
	script := `
def search(ctx):
    resp = ctx.http.get(ctx.inputs["base_url"] + "/tickets", auth = "bitbucket_pat")
    return resp.json()
`
	sidecar := `
kind: ticket_provider/v1
http:
  methods: [GET]
  hosts: [tickets.example.invalid]
auth:
  bitbucket_pat:
    env: BITBUCKET_PAT
    header: Authorization
    prefix: "Bearer "
    missing_code: missing_bitbucket_pat
    missing_message: "Bitbucket PAT is missing; run the environment setup script."
`
	path := writeProvider(t, script, sidecar)
	fake := &fakeHTTPClient{body: `{"tickets":[]}`}
	res, err := (&ticketprovider.StarlarkProvider{
		Script: path,
		HTTP:   fake,
		Env: func(context.Context, string) string {
			return ""
		},
	}).Invoke(context.Background(), "search", map[string]any{"base_url": "https://tickets.example.invalid"})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.Error == nil {
		t.Fatal("expected provider error")
	}
	if res.Error.Code != "missing_bitbucket_pat" {
		t.Fatalf("error code = %q, want missing_bitbucket_pat", res.Error.Code)
	}
	if fake.called {
		t.Fatal("HTTP transport must not be called when required auth env is missing")
	}
}

func TestStarlarkProvider_CustomScriptError(t *testing.T) {
	script := `
def get(ctx):
    resp = ctx.http.get(ctx.inputs["base_url"] + "/tickets/" + ctx.inputs["id"], auth = "zta")
    if resp.status == 401:
        return {
            "ok": False,
            "error": {
                "code": "zta_token_expired",
                "message": "ZTA token is expired.",
                "hint": "Run the environment setup script again.",
            },
        }
    return resp.json()
`
	sidecar := `
kind: ticket_provider/v1
http:
  methods: [GET]
  hosts: [tickets.example.invalid]
auth:
  zta:
    env: ZTA_TOKEN
    header: X-ZTA-Token
`
	path := writeProvider(t, script, sidecar)
	fake := &fakeHTTPClient{status: 401, body: `{"error":"expired"}`}
	res, err := (&ticketprovider.StarlarkProvider{
		Script: path,
		HTTP:   fake,
		Env: func(context.Context, string) string {
			return "zta-token"
		},
	}).Invoke(context.Background(), "get", map[string]any{
		"base_url": "https://tickets.example.invalid",
		"id":       "T-1",
	})
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if res.Error == nil {
		t.Fatal("expected provider error")
	}
	if res.Error.Code != "zta_token_expired" {
		t.Fatalf("error code = %q, want zta_token_expired", res.Error.Code)
	}
	if res.Error.Hint == "" {
		t.Fatal("expected custom error hint")
	}
}
