package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestJSONCarriesPermissionFloor(t *testing.T) {
	m := Manifest{
		Name:        "kitsoki-test-wizard",
		URL:         "https://example.test",
		WebhookURL:  "https://example.test/gh-agent/webhook",
		RedirectURL: "http://127.0.0.1:1234/callback",
	}
	raw, err := m.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	perms, ok := doc["default_permissions"].(map[string]any)
	if !ok {
		t.Fatalf("manifest missing default_permissions: %s", raw)
	}
	for k, want := range map[string]string{
		"issues": "write", "pull_requests": "write", "contents": "write", "checks": "read",
	} {
		if perms[k] != want {
			t.Errorf("default_permissions[%s] = %v, want %s", k, perms[k], want)
		}
	}
	hook, ok := doc["hook_attributes"].(map[string]any)
	if !ok || hook["url"] != m.WebhookURL || hook["active"] != true {
		t.Errorf("hook_attributes wrong: %v", doc["hook_attributes"])
	}
	if doc["public"] != false {
		t.Errorf("wizard apps must default private, got public=%v", doc["public"])
	}
}

func TestManifestJSONAllowsLocalOnlyOAuthSetup(t *testing.T) {
	m := Manifest{
		Name:        "kitsoki-local",
		URL:         "https://github.com/kitsoki",
		RedirectURL: "http://127.0.0.1:1234/callback",
	}
	raw, err := m.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if _, ok := doc["hook_attributes"]; ok {
		t.Fatalf("local-only manifest should not request hook_attributes: %s", raw)
	}
	if _, ok := doc["default_events"]; ok {
		t.Fatalf("local-only manifest should not subscribe to events: %s", raw)
	}
	callbacks, ok := doc["callback_urls"].([]any)
	if !ok || len(callbacks) != 1 || callbacks[0] != "http://127.0.0.1/callback" {
		t.Fatalf("local-only manifest should keep loopback callback: %v", doc["callback_urls"])
	}
	perms, ok := doc["default_permissions"].(map[string]any)
	if !ok || perms["issues"] != "write" || perms["metadata"] != "read" {
		t.Fatalf("local-only manifest permissions wrong: %v", doc["default_permissions"])
	}
}

func TestManifestJSONRejectsIncomplete(t *testing.T) {
	if _, err := (Manifest{Name: "x"}).JSON(); err == nil {
		t.Fatal("expected error for missing redirect url")
	}
}

func TestManifestPostURL(t *testing.T) {
	if got := ManifestPostURL("https://login.test", "", "s1"); got != "https://login.test/settings/apps/new?state=s1" {
		t.Errorf("user post url = %s", got)
	}
	if got := ManifestPostURL("https://login.test", "acme", "s2"); got != "https://login.test/organizations/acme/settings/apps/new?state=s2" {
		t.Errorf("org post url = %s", got)
	}
}

func TestConvertManifestCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app-manifests/CODE123/conversions" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":77,"slug":"kitsoki-wiz","client_id":"Iv1.abc","client_secret":"sec","webhook_secret":"hook","pem":"PEMDATA","html_url":"https://github.com/apps/kitsoki-wiz"}`)
	}))
	defer srv.Close()

	s := &SetupClient{HTTPClient: srv.Client(), APIBase: srv.URL}
	creds, err := s.ConvertManifestCode(context.Background(), "CODE123")
	if err != nil {
		t.Fatalf("ConvertManifestCode: %v", err)
	}
	if creds.ID != 77 || creds.Slug != "kitsoki-wiz" || creds.ClientID != "Iv1.abc" || creds.PEM != "PEMDATA" {
		t.Errorf("creds parsed wrong: %+v", creds)
	}
}

func TestConvertManifestCodeNon201(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	s := &SetupClient{HTTPClient: srv.Client(), APIBase: srv.URL}
	if _, err := s.ConvertManifestCode(context.Background(), "gone"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want 404 error, got %v", err)
	}
}

func TestWriteCredentialFilesAndInstallationUpdate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	creds := &AppCredentials{ID: 9, Slug: "s", ClientID: "cid", ClientSecret: "cs", WebhookSecret: "wh", PEM: "PEM"}
	envPath, pemPath, err := WriteCredentialFiles(dir, creds)
	if err != nil {
		t.Fatalf("WriteCredentialFiles: %v", err)
	}
	for _, p := range []string{envPath, pemPath} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s perms = %v, want 0600", p, info.Mode().Perm())
		}
	}
	raw, _ := os.ReadFile(envPath)
	for _, want := range []string{EnvAppID + "=9", EnvClientID + "=cid", EnvClientSecret + "=cs", EnvWebhookSecret + "=wh", EnvPrivateKeyFile + "=" + pemPath} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("env file missing %q:\n%s", want, raw)
		}
	}

	if err := UpdateEnvInstallationID(envPath, 4242); err != nil {
		t.Fatalf("UpdateEnvInstallationID: %v", err)
	}
	raw, _ = os.ReadFile(envPath)
	if !strings.Contains(string(raw), EnvInstallationID+"=4242") {
		t.Errorf("installation id not appended:\n%s", raw)
	}
	// Idempotent replace, not duplicate append.
	if err := UpdateEnvInstallationID(envPath, 4343); err != nil {
		t.Fatalf("UpdateEnvInstallationID replace: %v", err)
	}
	raw, _ = os.ReadFile(envPath)
	if strings.Count(string(raw), EnvInstallationID+"=") != 1 || !strings.Contains(string(raw), "=4343") {
		t.Errorf("installation id not replaced in place:\n%s", raw)
	}
}

func testKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

func TestWaitForInstallation(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing app JWT bearer")
		}
		calls++
		if calls < 2 {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprint(w, `[{"id":555}]`)
	}))
	defer srv.Close()

	s := &SetupClient{HTTPClient: srv.Client(), APIBase: srv.URL, Sleep: func(time.Duration) {}}
	id, err := s.WaitForInstallation(context.Background(), 9, testKeyPEM(t), time.Minute)
	if err != nil {
		t.Fatalf("WaitForInstallation: %v", err)
	}
	if id != 555 || calls != 2 {
		t.Errorf("id=%d calls=%d, want 555 after 2 polls", id, calls)
	}
}

func TestDeviceFlowTokenPolling(t *testing.T) {
	polls := 0
	var slept []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/device/code":
			fmt.Fprint(w, `{"device_code":"dev1","user_code":"ABCD-1234","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`)
		case "/login/oauth/access_token":
			polls++
			switch polls {
			case 1:
				fmt.Fprint(w, `{"error":"authorization_pending"}`)
			case 2:
				fmt.Fprint(w, `{"error":"slow_down","interval":2}`)
			default:
				fmt.Fprint(w, `{"access_token":"ghu_tok","token_type":"bearer","expires_in":28800}`)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	var prompt strings.Builder
	s := &SetupClient{
		HTTPClient: srv.Client(),
		LoginBase:  srv.URL,
		Out:        &prompt,
		Sleep:      func(d time.Duration) { slept = append(slept, d) },
	}
	tok, err := s.DeviceFlowToken(context.Background(), "Iv1.abc")
	if err != nil {
		t.Fatalf("DeviceFlowToken: %v", err)
	}
	if tok.AccessToken != "ghu_tok" {
		t.Errorf("token = %q", tok.AccessToken)
	}
	if !strings.Contains(prompt.String(), "ABCD-1234") {
		t.Errorf("prompt missing user code: %q", prompt.String())
	}
	if len(slept) != 3 || slept[2] != 2*time.Second {
		t.Errorf("slow_down did not raise the interval: %v", slept)
	}
	if tok.ExpiresAt.IsZero() {
		t.Errorf("expiring token should carry ExpiresAt")
	}
}

func TestDeviceFlowDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"device_flow_disabled"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	s := &SetupClient{HTTPClient: srv.Client(), LoginBase: srv.URL, Sleep: func(time.Duration) {}}
	_, err := s.DeviceFlowToken(context.Background(), "Iv1.abc")
	if err == nil || !strings.Contains(err.Error(), "Enable Device Flow") {
		t.Fatalf("want device-flow-disabled guidance, got %v", err)
	}
}

func TestExchangeWebFlowCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if r.Form.Get("client_secret") != "sec" || r.Form.Get("code") != "c0de" {
			t.Errorf("form missing fields: %v", r.Form)
		}
		fmt.Fprint(w, `{"access_token":"ghu_web","token_type":"bearer"}`)
	}))
	defer srv.Close()
	s := &SetupClient{HTTPClient: srv.Client(), LoginBase: srv.URL}
	tok, err := s.ExchangeWebFlowCode(context.Background(), "Iv1.abc", "sec", "c0de", "http://127.0.0.1:9/callback")
	if err != nil {
		t.Fatalf("ExchangeWebFlowCode: %v", err)
	}
	if tok.AccessToken != "ghu_web" || !tok.Valid() {
		t.Errorf("token wrong: %+v", tok)
	}
}

func TestUserTokenCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "user-token.json")
	tok := &UserToken{AccessToken: "ghu_x", TokenType: "bearer", ExpiresAt: time.Now().Add(time.Hour)}
	if err := SaveUserToken(path, tok); err != nil {
		t.Fatalf("SaveUserToken: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache perms: %v %v", info.Mode().Perm(), err)
	}
	got := LoadUserToken(path)
	if got == nil || got.AccessToken != "ghu_x" {
		t.Fatalf("LoadUserToken = %+v", got)
	}

	expired := &UserToken{AccessToken: "ghu_old", ExpiresAt: time.Now().Add(-time.Hour)}
	if err := SaveUserToken(path, expired); err != nil {
		t.Fatalf("SaveUserToken expired: %v", err)
	}
	if got := LoadUserToken(path); got != nil {
		t.Fatalf("expired token must not load, got %+v", got)
	}
}

func TestAttachRepoEndToEnd(t *testing.T) {
	var putPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer ghu_tok" {
			t.Errorf("auth header = %q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user/installations":
			fmt.Fprint(w, `{"installations":[{"id":142507898,"app_id":4141181,"app_slug":"kitsoki-test","account":{"login":"bsacrobatix"}}]}`)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/bsacrobatix/studio-sassfully":
			fmt.Fprint(w, `{"id":98765}`)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/user/installations/142507898/repositories/"):
			putPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/user/installations/142507898/repositories":
			fmt.Fprint(w, `{"total_count":2,"repositories":[{"full_name":"bsacrobatix/Kitsoki"},{"full_name":"bsacrobatix/studio-sassfully"}]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	s := &SetupClient{HTTPClient: srv.Client(), APIBase: srv.URL}

	installs, err := s.UserInstallations(ctx, "ghu_tok")
	if err != nil || len(installs) != 1 || installs[0].ID != 142507898 {
		t.Fatalf("UserInstallations = %+v, %v", installs, err)
	}
	repoID, err := s.RepoID(ctx, "ghu_tok", "bsacrobatix/studio-sassfully")
	if err != nil || repoID != 98765 {
		t.Fatalf("RepoID = %d, %v", repoID, err)
	}
	if err := s.AddRepoToInstallation(ctx, "ghu_tok", installs[0].ID, repoID); err != nil {
		t.Fatalf("AddRepoToInstallation: %v", err)
	}
	if putPath != "/user/installations/142507898/repositories/98765" {
		t.Errorf("PUT path = %s", putPath)
	}
	repos, err := s.InstallationRepos(ctx, "ghu_tok", installs[0].ID)
	if err != nil || len(repos) != 2 || repos[1] != "bsacrobatix/studio-sassfully" {
		t.Fatalf("InstallationRepos = %v, %v", repos, err)
	}
}

func TestAddRepoToInstallationErrorSurfacesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Must have admin rights"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	s := &SetupClient{HTTPClient: srv.Client(), APIBase: srv.URL}
	err := s.AddRepoToInstallation(context.Background(), "ghu_tok", 1, 2)
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "admin rights") {
		t.Fatalf("want 403 with body, got %v", err)
	}
}

func TestAddRepoToInstallationAlreadyAttachedIsIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/user/installations/142507898/repositories/98765":
			http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
		case r.Method == http.MethodGet && r.URL.Path == "/repositories/98765":
			fmt.Fprint(w, `{"id":98765,"full_name":"bsacrobatix/studio-sassfully"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/user/installations/142507898/repositories":
			fmt.Fprint(w, `{"total_count":1,"repositories":[{"full_name":"bsacrobatix/studio-sassfully"}]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	s := &SetupClient{HTTPClient: srv.Client(), APIBase: srv.URL}
	if err := s.AddRepoToInstallation(context.Background(), "ghu_tok", 142507898, 98765); err != nil {
		t.Fatalf("AddRepoToInstallation: want idempotent success, got %v", err)
	}
}

func TestAddRepoToInstallationForbiddenNotYetAttachedStillErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/user/installations/142507898/repositories/98765":
			http.Error(w, `{"message":"Resource not accessible by integration"}`, http.StatusForbidden)
		case r.Method == http.MethodGet && r.URL.Path == "/repositories/98765":
			fmt.Fprint(w, `{"id":98765,"full_name":"bsacrobatix/studio-sassfully"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/user/installations/142507898/repositories":
			fmt.Fprint(w, `{"total_count":0,"repositories":[]}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	s := &SetupClient{HTTPClient: srv.Client(), APIBase: srv.URL}
	err := s.AddRepoToInstallation(context.Background(), "ghu_tok", 142507898, 98765)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want 403 error when repo isn't actually attached, got %v", err)
	}
}
