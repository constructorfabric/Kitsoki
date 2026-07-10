// App-side setup flows for `kitsoki gh-agent setup` — the packaging plan's
// Tier-1 "hero path" (.context/gh-agent-packaging-research.md §2A/§3):
//
//   - the App-Manifest one-click: serve a local auto-submitting form that
//     POSTs a manifest to GitHub's app-creation page, catch the redirect
//     code, and exchange it at /app-manifests/{code}/conversions for the
//     App's FULL credentials (id, slug, client id/secret, webhook secret,
//     .pem) — no settings-page copy/paste;
//   - user-to-server token acquisition (OAuth web flow against a localhost
//     redirect, or the device flow when no client secret is at hand) — the
//     token class GitHub requires for /user/installations endpoints;
//   - installation repository management with that token, so attaching a
//     repo to the App installation is a CLI call, not a settings walk.
//
// Same rules as the rest of the package: stdlib only, the HTTP surface is an
// injected Doer, and tests run with ZERO network.
package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Env var names for the App's OAuth client identity, written by `setup app`
// and read by `setup attach`. The client secret is a secret; it lives in the
// 0600 env file, and is only ever read from env/file, never logged.
const (
	EnvClientID     = "KITSOKI_GH_APP_CLIENT_ID"
	EnvClientSecret = "KITSOKI_GH_APP_CLIENT_SECRET"
)

// githubLoginBase is the browser/login host (device + web OAuth flows live
// here, not on api.github.com).
const githubLoginBase = "https://github.com"

// SetupClient bundles the injectable surfaces the setup flows need. The zero
// value is production-ready; tests override the bases with httptest servers.
type SetupClient struct {
	HTTPClient Doer                // nil → http.DefaultClient
	APIBase    string              // "" → https://api.github.com
	LoginBase  string              // "" → https://github.com
	Sleep      func(time.Duration) // nil → time.Sleep (device-flow polling)
	Out        io.Writer           // progress/prompts; nil → io.Discard
}

func (s *SetupClient) doer() Doer {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	return http.DefaultClient
}

func (s *SetupClient) api() string {
	if s.APIBase != "" {
		return strings.TrimSuffix(s.APIBase, "/")
	}
	return githubAPIBase
}

func (s *SetupClient) login() string {
	if s.LoginBase != "" {
		return strings.TrimSuffix(s.LoginBase, "/")
	}
	return githubLoginBase
}

func (s *SetupClient) sleep(d time.Duration) {
	if s.Sleep != nil {
		s.Sleep(d)
		return
	}
	time.Sleep(d)
}

func (s *SetupClient) out() io.Writer {
	if s.Out != nil {
		return s.Out
	}
	return io.Discard
}

// --- App-Manifest one-click ------------------------------------------------

// Manifest describes the GitHub App to create. Permissions are fixed to the
// @kitsoki agent's floor (kitsoki-github-agent proposal, shared decision #1) —
// issues/PRs/contents write, checks read — so every wizard-created App is born
// least-privilege. WebhookURL is optional for local-only OAuth/token setup; when
// empty, no webhook or event subscription is requested.
type Manifest struct {
	Name        string
	URL         string // the App's homepage
	WebhookURL  string // optional; empty creates a local-only OAuth/App-token profile
	RedirectURL string // local callback that receives the manifest code
	Description string
	// Public false = only installable on the owning account, the right
	// default for a per-operator agent App.
	Public bool
}

// JSON renders the manifest document GitHub's app-creation page consumes.
func (m Manifest) JSON() ([]byte, error) {
	if m.Name == "" {
		return nil, fmt.Errorf("githubapp: manifest needs a name")
	}
	if m.RedirectURL == "" {
		return nil, fmt.Errorf("githubapp: manifest needs a redirect url")
	}
	doc := map[string]any{
		"name":         m.Name,
		"url":          m.URL,
		"redirect_url": m.RedirectURL,
		// OAuth callback for the later `setup attach` web flow. GitHub
		// ignores the port on loopback callback URLs, so one entry covers
		// every ephemeral local listener.
		"callback_urls": []string{"http://127.0.0.1/callback"},
		"description":   m.Description,
		"public":        m.Public,
		"default_permissions": map[string]string{
			"issues":        "write",
			"pull_requests": "write",
			"contents":      "write",
			"checks":        "read",
			"metadata":      "read",
		},
	}
	if m.WebhookURL != "" {
		doc["hook_attributes"] = map[string]any{
			"url":    m.WebhookURL,
			"active": true,
		}
		doc["default_events"] = []string{
			"issues",
			"issue_comment",
			"pull_request",
			"pull_request_review",
			"pull_request_review_comment",
			"check_suite",
		}
	}
	return json.Marshal(doc)
}

// ManifestPostURL is the GitHub page that accepts the manifest form: the
// user's settings by default, an organization's when org is non-empty.
func ManifestPostURL(loginBase, org, state string) string {
	base := strings.TrimSuffix(loginBase, "/")
	if base == "" {
		base = githubLoginBase
	}
	path := "/settings/apps/new"
	if org != "" {
		path = "/organizations/" + url.PathEscape(org) + "/settings/apps/new"
	}
	return base + path + "?state=" + url.QueryEscape(state)
}

// ManifestFormHTML renders the local page that auto-POSTs the manifest to
// GitHub. GitHub then shows ONE confirmation page with ONE button ("Create
// GitHub App") — that button is the single click the whole flow costs.
func ManifestFormHTML(postURL string, manifestJSON []byte) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><title>kitsoki gh-agent setup</title></head>
<body onload="document.forms[0].submit()">
<p>Sending the App manifest to GitHub&hellip; if nothing happens,</p>
<form action="%s" method="post">
  <input type="hidden" name="manifest" value="%s">
  <button type="submit">continue to GitHub</button>
</form>
</body></html>`, html.EscapeString(postURL), html.EscapeString(string(manifestJSON)))
}

// AppCredentials is the manifest-conversion response: everything the agent
// needs to run, delivered programmatically.
type AppCredentials struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	WebhookSecret string `json:"webhook_secret"`
	PEM           string `json:"pem"`
	HTMLURL       string `json:"html_url"`
}

// ConvertManifestCode exchanges the redirect code from the app-creation page
// for the new App's credentials. The code is single-use and expires in one
// hour; GitHub answers 201 on success.
func (s *SetupClient) ConvertManifestCode(ctx context.Context, code string) (*AppCredentials, error) {
	u := fmt.Sprintf("%s/app-manifests/%s/conversions", s.api(), url.PathEscape(code))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return nil, fmt.Errorf("githubapp: build manifest-conversion request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := s.doer().Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: manifest conversion: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("githubapp: manifest conversion returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var creds AppCredentials
	if err := json.Unmarshal(body, &creds); err != nil {
		return nil, fmt.Errorf("githubapp: parse manifest conversion response: %w", err)
	}
	if creds.ID == 0 || creds.PEM == "" {
		return nil, fmt.Errorf("githubapp: manifest conversion response is missing id or pem")
	}
	return &creds, nil
}

// WriteCredentialFiles persists the conversion result under dir (created
// 0700): gh-app.pem and kitsoki.env, both 0600. The env file carries the
// KITSOKI_GH_APP_* names the serve/poll paths already read; the installation
// id line is appended later by UpdateEnvInstallationID once the App is
// installed somewhere.
func WriteCredentialFiles(dir string, creds *AppCredentials) (envPath, pemPath string, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("githubapp: create credential dir: %w", err)
	}
	pemPath = filepath.Join(dir, "gh-app.pem")
	if err := os.WriteFile(pemPath, []byte(creds.PEM), 0o600); err != nil {
		return "", "", fmt.Errorf("githubapp: write pem: %w", err)
	}
	envPath = filepath.Join(dir, "kitsoki.env")
	env := fmt.Sprintf(`# GitHub App credentials for %q — written by kitsoki gh-agent setup.
# Keep private (0600). Source into the gh-agent service environment.
%s=%d
%s=%s
%s=%s
%s=%s
%s=%s
`,
		creds.Slug,
		EnvAppID, creds.ID,
		EnvPrivateKeyFile, pemPath,
		EnvWebhookSecret, creds.WebhookSecret,
		EnvClientID, creds.ClientID,
		EnvClientSecret, creds.ClientSecret,
	)
	if err := os.WriteFile(envPath, []byte(env), 0o600); err != nil {
		return "", "", fmt.Errorf("githubapp: write env file: %w", err)
	}
	return envPath, pemPath, nil
}

// UpdateEnvInstallationID sets KITSOKI_GH_APP_INSTALLATION_ID in the env file
// written by WriteCredentialFiles, replacing an existing line or appending.
func UpdateEnvInstallationID(envPath string, installationID int64) error {
	raw, err := os.ReadFile(envPath)
	if err != nil {
		return fmt.Errorf("githubapp: read env file: %w", err)
	}
	line := fmt.Sprintf("%s=%d", EnvInstallationID, installationID)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	replaced := false
	for i, l := range lines {
		if strings.HasPrefix(l, EnvInstallationID+"=") {
			lines[i] = line
			replaced = true
		}
	}
	if !replaced {
		lines = append(lines, line)
	}
	return os.WriteFile(envPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// WaitForInstallation polls GET /app/installations (authenticated as the App
// via its JWT) until the operator finishes GitHub's install-consent page,
// then returns the new installation's id. Poll cadence is 3s via Sleep.
func (s *SetupClient) WaitForInstallation(ctx context.Context, appID int64, pemBytes []byte, timeout time.Duration) (int64, error) {
	key, err := parseRSAPrivateKey(pemBytes)
	if err != nil {
		return 0, err
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		jwt, err := signAppJWT(appID, key, time.Now())
		if err != nil {
			return 0, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.api()+"/app/installations", nil)
		if err != nil {
			return 0, fmt.Errorf("githubapp: build installations request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := s.doer().Do(req)
		if err != nil {
			return 0, fmt.Errorf("githubapp: list app installations: %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var installs []struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(body, &installs); err != nil {
				return 0, fmt.Errorf("githubapp: parse app installations: %w", err)
			}
			if len(installs) > 0 {
				return installs[0].ID, nil
			}
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("githubapp: no installation appeared within %s — finish the install page and re-run", timeout)
		}
		s.sleep(3 * time.Second)
	}
}

// --- user-to-server tokens ---------------------------------------------------

// UserToken is a user-to-server token (ghu_…) minted by the App on the
// operator's behalf — the credential class /user/installations requires.
type UserToken struct {
	AccessToken string    `json:"access_token"`
	TokenType   string    `json:"token_type"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

// Valid reports whether the token exists and (when it expires at all) has at
// least a minute of life left.
func (t *UserToken) Valid() bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	if t.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Add(time.Minute).Before(t.ExpiresAt)
}

// SaveUserToken caches the token at path (0600, parent dir 0700).
func SaveUserToken(path string, t *UserToken) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("githubapp: create token cache dir: %w", err)
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("githubapp: marshal user token: %w", err)
	}
	return os.WriteFile(path, raw, 0o600)
}

// LoadUserToken returns the cached token at path, or nil when the file is
// absent, unreadable, or the token is no longer Valid.
func LoadUserToken(path string) *UserToken {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var t UserToken
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil
	}
	if !t.Valid() {
		return nil
	}
	return &t
}

// AuthorizeURL is the web-flow authorization page for the App's OAuth client.
func AuthorizeURL(loginBase, clientID, redirectURI, state string) string {
	base := strings.TrimSuffix(loginBase, "/")
	if base == "" {
		base = githubLoginBase
	}
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	return base + "/login/oauth/authorize?" + q.Encode()
}

// RandomState returns a hex nonce for OAuth state parameters.
func RandomState() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("githubapp: random state: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

type oauthTokenResponse struct {
	AccessToken      string `json:"access_token"`
	TokenType        string `json:"token_type"`
	ExpiresIn        int64  `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	Interval         int64  `json:"interval"`
}

func (s *SetupClient) postLoginForm(ctx context.Context, path string, form url.Values) (*oauthTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.login()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("githubapp: build %s request: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.doer().Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out oauthTokenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("githubapp: parse %s response (%d): %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return &out, nil
}

func (r *oauthTokenResponse) toToken() *UserToken {
	t := &UserToken{AccessToken: r.AccessToken, TokenType: r.TokenType}
	if r.ExpiresIn > 0 {
		t.ExpiresAt = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	}
	return t
}

// ExchangeWebFlowCode trades a web-flow authorization code for a user token.
func (s *SetupClient) ExchangeWebFlowCode(ctx context.Context, clientID, clientSecret, code, redirectURI string) (*UserToken, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	out, err := s.postLoginForm(ctx, "/login/oauth/access_token", form)
	if err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("githubapp: web-flow exchange failed: %s (%s)", out.Error, out.ErrorDescription)
	}
	if out.AccessToken == "" {
		return nil, fmt.Errorf("githubapp: web-flow exchange returned no token")
	}
	return out.toToken(), nil
}

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int64  `json:"expires_in"`
	Interval        int64  `json:"interval"`
	Error           string `json:"error"`
}

// DeviceFlowToken runs the OAuth device flow: it prints the one-time code and
// verification URL to Out, then polls until the operator approves. Requires
// "Enable Device Flow" on the App (Settings → Developer settings → the App →
// General) — the error says so when it isn't.
func (s *SetupClient) DeviceFlowToken(ctx context.Context, clientID string) (*UserToken, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.login()+"/login/device/code", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("githubapp: build device-code request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.doer().Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: device-code request: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	var dc deviceCodeResponse
	if err := json.Unmarshal(body, &dc); err != nil {
		return nil, fmt.Errorf("githubapp: parse device-code response (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if dc.Error == "device_flow_disabled" || (dc.DeviceCode == "" && resp.StatusCode != http.StatusOK) {
		return nil, fmt.Errorf("githubapp: device flow is not enabled for this App — either enable it (App settings → General → Enable Device Flow) or provide the client secret so the web flow can run (%s)", strings.TrimSpace(string(body)))
	}
	if dc.DeviceCode == "" {
		return nil, fmt.Errorf("githubapp: device-code response missing device_code")
	}

	fmt.Fprintf(s.out(), "To authorize, visit %s and enter the code: %s\n", dc.VerificationURI, dc.UserCode)

	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if dc.ExpiresIn > 0 && time.Now().After(deadline) {
			return nil, fmt.Errorf("githubapp: device code expired before authorization")
		}
		s.sleep(interval)

		pf := url.Values{}
		pf.Set("client_id", clientID)
		pf.Set("device_code", dc.DeviceCode)
		pf.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		out, err := s.postLoginForm(ctx, "/login/oauth/access_token", pf)
		if err != nil {
			return nil, err
		}
		switch out.Error {
		case "":
			if out.AccessToken == "" {
				return nil, fmt.Errorf("githubapp: device flow returned no token")
			}
			return out.toToken(), nil
		case "authorization_pending":
			continue
		case "slow_down":
			extra := time.Duration(out.Interval) * time.Second
			if extra <= 0 {
				extra = interval + 5*time.Second
			}
			interval = extra
		case "expired_token":
			return nil, fmt.Errorf("githubapp: device code expired before authorization")
		case "access_denied":
			return nil, fmt.Errorf("githubapp: authorization was denied")
		default:
			return nil, fmt.Errorf("githubapp: device flow failed: %s (%s)", out.Error, out.ErrorDescription)
		}
	}
}

// --- installation repository management ---------------------------------------

// InstallationInfo is one row of GET /user/installations.
type InstallationInfo struct {
	ID      int64  `json:"id"`
	AppID   int64  `json:"app_id"`
	AppSlug string `json:"app_slug"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
}

func (s *SetupClient) userAPI(ctx context.Context, method, path, token string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, method, s.api()+path, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("githubapp: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := s.doer().Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("githubapp: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return body, resp.StatusCode, nil
}

// UserInstallations lists the App installations the token's user can reach.
func (s *SetupClient) UserInstallations(ctx context.Context, token string) ([]InstallationInfo, error) {
	body, status, err := s.userAPI(ctx, http.MethodGet, "/user/installations?per_page=100", token)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("githubapp: list user installations returned %d: %s", status, strings.TrimSpace(string(body)))
	}
	var out struct {
		Installations []InstallationInfo `json:"installations"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("githubapp: parse user installations: %w", err)
	}
	return out.Installations, nil
}

// RepoID resolves owner/name to GitHub's numeric repository id.
func (s *SetupClient) RepoID(ctx context.Context, token, slug string) (int64, error) {
	body, status, err := s.userAPI(ctx, http.MethodGet, "/repos/"+slug, token)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("githubapp: repo %s lookup returned %d: %s", slug, status, strings.TrimSpace(string(body)))
	}
	var repo struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &repo); err != nil || repo.ID == 0 {
		return 0, fmt.Errorf("githubapp: parse repo %s id: %w", slug, err)
	}
	return repo.ID, nil
}

// AddRepoToInstallation attaches a repository to the installation — the
// programmatic equivalent of the settings-page "Repository access" walk.
// GitHub answers 204 whether the repo was newly added or already selected,
// but some installations instead answer 403 "Resource not accessible by
// integration" when the repo is already attached. Treat that specific 403 as
// idempotent success once we confirm the repo is already in the
// installation's repository list, rather than surfacing it as a hard failure.
func (s *SetupClient) AddRepoToInstallation(ctx context.Context, token string, installationID, repoID int64) error {
	path := fmt.Sprintf("/user/installations/%d/repositories/%d", installationID, repoID)
	body, status, err := s.userAPI(ctx, http.MethodPut, path, token)
	if err != nil {
		return err
	}
	if status == http.StatusNoContent {
		return nil
	}
	if status == http.StatusForbidden && strings.Contains(string(body), "Resource not accessible by integration") {
		if already, checkErr := s.repoAlreadyInInstallation(ctx, token, installationID, repoID); checkErr == nil && already {
			return nil
		}
	}
	return fmt.Errorf("githubapp: add repo to installation returned %d: %s", status, strings.TrimSpace(string(body)))
}

// repoAlreadyInInstallation reports whether repoID is already visible to
// installationID, by resolving repoID to its full name and checking it
// against the installation's repository list.
func (s *SetupClient) repoAlreadyInInstallation(ctx context.Context, token string, installationID, repoID int64) (bool, error) {
	body, status, err := s.userAPI(ctx, http.MethodGet, fmt.Sprintf("/repositories/%d", repoID), token)
	if err != nil {
		return false, err
	}
	if status != http.StatusOK {
		return false, fmt.Errorf("githubapp: repo id %d lookup returned %d: %s", repoID, status, strings.TrimSpace(string(body)))
	}
	var repo struct {
		FullName string `json:"full_name"`
	}
	if err := json.Unmarshal(body, &repo); err != nil || repo.FullName == "" {
		return false, fmt.Errorf("githubapp: parse repo id %d: %w", repoID, err)
	}
	names, err := s.InstallationRepos(ctx, token, installationID)
	if err != nil {
		return false, err
	}
	for _, name := range names {
		if name == repo.FullName {
			return true, nil
		}
	}
	return false, nil
}

// InstallationRepos lists the full names the installation can currently see.
func (s *SetupClient) InstallationRepos(ctx context.Context, token string, installationID int64) ([]string, error) {
	var names []string
	for page := 1; ; page++ {
		path := fmt.Sprintf("/user/installations/%d/repositories?per_page=100&page=%d", installationID, page)
		body, status, err := s.userAPI(ctx, http.MethodGet, path, token)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("githubapp: list installation repos returned %d: %s", status, strings.TrimSpace(string(body)))
		}
		var out struct {
			Repositories []struct {
				FullName string `json:"full_name"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return nil, fmt.Errorf("githubapp: parse installation repos: %w", err)
		}
		for _, r := range out.Repositories {
			names = append(names, r.FullName)
		}
		if len(out.Repositories) < 100 {
			return names, nil
		}
	}
}
