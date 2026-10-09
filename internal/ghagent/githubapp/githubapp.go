// Package githubapp mints GitHub App installation tokens and exposes them to
// the native GitHub host surfaces used by the @kitsoki agent.
//
// The cassette-backed @kitsoki loop (internal/ghagent + `kitsoki gh-agent
// poll`) routes GitHub issue/PR I/O through host.gh.ticket and host.git. Those
// providers use the native GitHub REST API for GitHub operations and read their
// credential from GH_TOKEN / GITHUB_TOKEN. This package's only job is therefore
// to produce that credential when the agent runs against LIVE GitHub as a
// GitHub App installation:
//
//   - AppJWT signs a short-lived RS256 JWT as the App (iss=AppID) using the
//     App's RSA private key (read from a FILE, never inlined in env).
//   - AppTokenSource exchanges that JWT for an installation access token via
//     POST /app/installations/<id>/access_tokens, caching it until shortly
//     before expiry.
//   - WithGHToken resolves a token and runs a function with GH_TOKEN set, so
//     host GitHub API calls authenticate as the installation.
//
// Crypto is stdlib only (crypto/rsa + crypto/sha256 + encoding/base64/json) —
// no third-party JWT dependency. The HTTP client is an injected Doer so tests
// run with ZERO network. VerifyWebhookSignature is shipped now for the
// round-2 webhook ingress.
//
// See docs/guide/integrations/github-app-setup.md for the operator runbook and
// docs/proposals/kitsoki-github-agent.md (shared decision #1) for the auth
// decision and permissions floor.
package githubapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Env var names read by LoadConfigFromEnv. Private keys are referenced by file
// path (KITSOKI_GH_APP_PRIVATE_KEY_FILE), never inlined into the environment.
const (
	EnvAppID          = "KITSOKI_GH_APP_ID"
	EnvInstallationID = "KITSOKI_GH_APP_INSTALLATION_ID"
	EnvPrivateKeyFile = "KITSOKI_GH_APP_PRIVATE_KEY_FILE"
	EnvWebhookSecret  = "KITSOKI_GH_WEBHOOK_SECRET"
)

// githubAPIBase is the GitHub REST base; overridable only for tests via the
// injected Doer (which never reaches the real host).
const githubAPIBase = "https://api.github.com"

// Config holds the GitHub App identity needed to mint installation tokens.
type Config struct {
	AppID          int64
	InstallationID int64
	PrivateKeyPath string
	// WebhookSecret authenticates round-2 webhook payloads (HMAC-SHA256). It
	// is optional for round-1 poll mode.
	WebhookSecret string
}

// LoadConfigFromEnv reads the KITSOKI_GH_APP_* / KITSOKI_GH_WEBHOOK_SECRET
// environment variables. It returns (nil, nil) when NONE of the App vars are
// set (the offline/cassette path stays unconfigured), and a clear error when
// the config is only half-populated.
func LoadConfigFromEnv() (*Config, error) {
	appID := os.Getenv(EnvAppID)
	instID := os.Getenv(EnvInstallationID)
	keyFile := os.Getenv(EnvPrivateKeyFile)
	secret := os.Getenv(EnvWebhookSecret)

	if appID == "" && instID == "" && keyFile == "" {
		// Not configured for App auth — offline/PAT path. Not an error.
		return nil, nil
	}

	var missing []string
	if appID == "" {
		missing = append(missing, EnvAppID)
	}
	if instID == "" {
		missing = append(missing, EnvInstallationID)
	}
	if keyFile == "" {
		missing = append(missing, EnvPrivateKeyFile)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("githubapp: GitHub App auth is half-configured; missing %s", strings.Join(missing, ", "))
	}

	app, err := strconv.ParseInt(appID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("githubapp: %s must be an integer: %w", EnvAppID, err)
	}
	inst, err := strconv.ParseInt(instID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("githubapp: %s must be an integer: %w", EnvInstallationID, err)
	}

	cfg := &Config{
		AppID:          app,
		InstallationID: inst,
		PrivateKeyPath: keyFile,
		WebhookSecret:  secret,
	}
	return cfg, cfg.Validate()
}

// Validate checks the config is structurally complete (it does not read the
// key file or touch the network).
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("githubapp: nil config")
	}
	if c.AppID <= 0 {
		return fmt.Errorf("githubapp: AppID must be positive")
	}
	if c.InstallationID <= 0 {
		return fmt.Errorf("githubapp: InstallationID must be positive")
	}
	if c.PrivateKeyPath == "" {
		return fmt.Errorf("githubapp: PrivateKeyPath is required")
	}
	return nil
}

// loadPrivateKey reads and parses the App's RSA private key from a PEM file
// (PKCS#1 "RSA PRIVATE KEY" or PKCS#8 "PRIVATE KEY").
func loadPrivateKey(path string) (*rsa.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("githubapp: read private key %q: %w", path, err)
	}
	return parseRSAPrivateKey(raw)
}

func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("githubapp: no PEM block found in private key")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("githubapp: parse private key (tried PKCS1 and PKCS8): %w", err)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("githubapp: private key is not an RSA key (got %T)", parsed)
	}
	return rsaKey, nil
}

// b64url is the RFC-7515 base64url encoding (no padding) used in JWTs.
func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// AppJWT returns an RS256-signed App JWT for cfg, valid for ~9 minutes. iat is
// backdated 60s for clock skew (GitHub rejects future-iat / >10min-exp JWTs).
func AppJWT(cfg *Config, now time.Time) (string, error) {
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	key, err := loadPrivateKey(cfg.PrivateKeyPath)
	if err != nil {
		return "", err
	}
	return signAppJWT(cfg.AppID, key, now)
}

// signAppJWT builds the JWT given an already-parsed key (split out so tests can
// sign without a file).
func signAppJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss": appID,
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("githubapp: marshal jwt header: %w", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("githubapp: marshal jwt claims: %w", err)
	}
	signingInput := b64url(hb) + "." + b64url(cb)

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("githubapp: sign jwt: %w", err)
	}
	return signingInput + "." + b64url(sig), nil
}

// Doer is the minimal HTTP surface AppTokenSource needs. http.DefaultClient
// satisfies it; tests inject a fake to stay offline.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// TokenSource yields a GitHub installation token usable as GH_TOKEN.
type TokenSource interface {
	InstallationToken(ctx context.Context) (token string, expiry time.Time, err error)
}

// staticTokenSource returns a fixed token (no expiry-driven refresh).
type staticTokenSource struct{ token string }

// StaticTokenSource returns a TokenSource that always yields token. Used by the
// offline/cassette path and by tests (e.g. a PAT or a dummy).
func StaticTokenSource(token string) TokenSource { return staticTokenSource{token: token} }

func (s staticTokenSource) InstallationToken(context.Context) (string, time.Time, error) {
	return s.token, time.Time{}, nil
}

// AppTokenSource mints and caches installation tokens for a Config.
type AppTokenSource struct {
	cfg *Config
	// HTTPClient is injected; nil defaults to http.DefaultClient.
	HTTPClient Doer
	// Now is injected for tests; nil defaults to time.Now.
	Now func() time.Time

	mu          sync.Mutex
	cachedToken string
	cachedExp   time.Time
}

// NewAppTokenSource builds an AppTokenSource. A nil client defaults to
// http.DefaultClient.
func NewAppTokenSource(cfg *Config, client Doer) (*AppTokenSource, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &AppTokenSource{cfg: cfg, HTTPClient: client, Now: time.Now}, nil
}

func (a *AppTokenSource) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// refreshSkew is how long before real expiry a cached token is considered
// stale and re-minted.
const refreshSkew = time.Minute

type accessTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// InstallationToken returns a cached token when one is valid for at least
// refreshSkew, otherwise mints a fresh one via the access_tokens endpoint.
func (a *AppTokenSource) InstallationToken(ctx context.Context) (string, time.Time, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := a.now()
	if a.cachedToken != "" && now.Add(refreshSkew).Before(a.cachedExp) {
		return a.cachedToken, a.cachedExp, nil
	}

	jwt, err := signCurrentAppJWT(a.cfg, now)
	if err != nil {
		return "", time.Time{}, err
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPIBase, a.cfg.InstallationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("githubapp: build access-token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.HTTPClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("githubapp: POST access_tokens: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("githubapp: read access-token response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("githubapp: access_tokens returned %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}

	var parsed accessTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", time.Time{}, fmt.Errorf("githubapp: parse access-token response: %w", err)
	}
	if parsed.Token == "" {
		return "", time.Time{}, fmt.Errorf("githubapp: access_tokens response had empty token")
	}

	a.cachedToken = parsed.Token
	a.cachedExp = parsed.ExpiresAt
	return parsed.Token, parsed.ExpiresAt, nil
}

// signCurrentAppJWT signs a JWT for cfg at now. Split out so AppTokenSource
// can reuse the parsed key indirectly via the file each refresh (key rotation
// safe) while remaining trivially mockable through the Doer for the HTTP leg.
func signCurrentAppJWT(cfg *Config, now time.Time) (string, error) {
	return AppJWT(cfg, now)
}

// VerifyWebhookSignature reports whether sigHeader is a valid
// X-Hub-Signature-256 ("sha256=<hex>") HMAC-SHA256 of body under secret. The
// comparison is constant-time. An empty secret or malformed header is invalid.
func VerifyWebhookSignature(secret string, body []byte, sigHeader string) bool {
	if secret == "" {
		return false
	}
	const prefix = "sha256="
	if !strings.HasPrefix(sigHeader, prefix) {
		return false
	}
	want := computeWebhookSignature(secret, body)
	return hmac.Equal([]byte(want), []byte(sigHeader))
}

// computeWebhookSignature returns the "sha256=<hex>" signature for body.
func computeWebhookSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + fmt.Sprintf("%x", mac.Sum(nil))
}

// WithGHToken resolves an installation token from ts and runs fn with the
// GH_TOKEN env var set to it, restoring the previous value afterward. This is
// how the App credential reaches host GitHub API calls without requiring a
// locally authenticated gh CLI.
func WithGHToken(ctx context.Context, ts TokenSource, fn func() error) error {
	token, _, err := ts.InstallationToken(ctx)
	if err != nil {
		return err
	}
	prev, had := os.LookupEnv("GH_TOKEN")
	if err := os.Setenv("GH_TOKEN", token); err != nil {
		return fmt.Errorf("githubapp: set GH_TOKEN: %w", err)
	}
	defer func() {
		if had {
			_ = os.Setenv("GH_TOKEN", prev)
		} else {
			_ = os.Unsetenv("GH_TOKEN")
		}
	}()
	return fn()
}
