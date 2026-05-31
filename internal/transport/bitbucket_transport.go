package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// BitbucketConfig configures the Bitbucket transport.
type BitbucketConfig struct {
	// BaseURL is the Bitbucket REST root.  Defaults to the Acronis ZTA
	// proxy mount when empty, mirroring tools/loopy's
	// `_DEFAULT_API_BASE = "https://localhost:3128/bitbucket"`.
	BaseURL string
	// Token is the Bitbucket personal access token presented via Bearer.
	Token string
	// BotMarker overrides DefaultBotMarker for this transport.
	BotMarker string
	// HTTPClient is used to make REST calls.  nil → a client with a 30s
	// timeout and TLS verification disabled (suitable for the ZTA proxy's
	// self-signed cert).
	HTTPClient *http.Client
}

// BitbucketTransport posts comments to a Bitbucket Server (Stash) pull
// request over the REST API, authenticating with a Bearer personal-access
// token. Unlike Jira, the PR is addressed by three coordinates that do not
// fit [SessionKey] — pr_project, pr_slug, pr_id — so they ride in
// [Message].Extra (see the package Routing section); SessionKey.Thread is
// kept only for orchestrator-side correlation. The default deployment targets
// the Acronis ZTA proxy ([DefaultBitbucketBaseURL]), which presents a
// self-signed cert, so the default HTTP client skips TLS verification; callers
// wanting strict verification inject their own *http.Client via
// [BitbucketConfig].HTTPClient. The zero value is not usable; construct via
// [NewBitbucketTransport].
type BitbucketTransport struct {
	cfg    BitbucketConfig
	client *http.Client
}

// DefaultBitbucketBaseURL is the Acronis ZTA-proxy mount that
// tools/loopy's bitbucket_io speaks to.  Stays in sync with
// pr_refine.bitbucket_io._DEFAULT_API_BASE so both surfaces target the
// same proxy.
const DefaultBitbucketBaseURL = "https://localhost:3128/bitbucket"

// NewBitbucketTransport constructs a BitbucketTransport.  Returns an error
// when Token is empty; BaseURL defaults to DefaultBitbucketBaseURL.
func NewBitbucketTransport(cfg BitbucketConfig) (*BitbucketTransport, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, fmt.Errorf("bitbucket: Token is required")
	}
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = DefaultBitbucketBaseURL
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.BotMarker == "" {
		cfg.BotMarker = DefaultBotMarker
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: httpClientTimeout,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		}
	}
	return &BitbucketTransport{cfg: cfg, client: client}, nil
}

// ID reports the transport ID.  Always "bitbucket".
func (b *BitbucketTransport) ID() string { return "bitbucket" }

// Post posts a comment to the PR identified by msg.Extra["pr_project"],
// msg.Extra["pr_slug"], msg.Extra["pr_id"].  Returns the Bitbucket-
// assigned comment ID as a decimal string.
//
// Non-2xx responses surface as an error containing the response body so
// the orchestrator's on_error arc carries a diagnosable message.
func (b *BitbucketTransport) Post(ctx context.Context, _ SessionKey, msg Message) (string, error) {
	project, slug, prID, err := bitbucketCoordsFromExtra(msg.Extra)
	if err != nil {
		return "", err
	}

	body := buildBitbucketBody(msg, b.cfg.BotMarker)

	url := fmt.Sprintf(
		"%s/rest/api/1.0/projects/%s/repos/%s/pull-requests/%s/comments",
		b.cfg.BaseURL, project, slug, prID,
	)

	payload := map[string]any{"text": body}
	enc, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("bitbucket: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(enc))
	if err != nil {
		return "", fmt.Errorf("bitbucket: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.cfg.Token)

	resp, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("bitbucket: do: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		summary := strings.TrimSpace(string(respBody))
		if summary == "" {
			summary = resp.Status
		}
		return "", fmt.Errorf("bitbucket: POST %s: %s: %s", url, resp.Status, summary)
	}

	// Bitbucket returns the new comment object as JSON; `id` is numeric.
	var parsed struct {
		ID json.Number `json:"id"`
	}
	dec := json.NewDecoder(bytes.NewReader(respBody))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		return "", fmt.Errorf("bitbucket: parse response: %w (body=%s)", err, string(respBody))
	}
	return parsed.ID.String(), nil
}

// Close releases idle HTTP connections held by the transport client.
func (b *BitbucketTransport) Close() error {
	if t, ok := b.client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	return nil
}

// bitbucketCoordsFromExtra extracts (project, slug, pr_id) from msg.Extra
// and returns a single clean error when any are missing.  pr_id is kept
// as a string because the REST URL path takes it verbatim — coercion to
// int would just round-trip back to text.
func bitbucketCoordsFromExtra(extra map[string]string) (project, slug, prID string, err error) {
	if extra == nil {
		return "", "", "", fmt.Errorf("bitbucket: pr_project, pr_slug, pr_id are required (none supplied)")
	}
	project = strings.TrimSpace(extra["pr_project"])
	slug = strings.TrimSpace(extra["pr_slug"])
	prID = strings.TrimSpace(extra["pr_id"])
	if project == "" || slug == "" || prID == "" {
		return "", "", "", fmt.Errorf(
			"bitbucket: pr_project=%q pr_slug=%q pr_id=%q — all three are required",
			project, slug, prID,
		)
	}
	return project, slug, prID, nil
}

// buildBitbucketBody composes the comment text.  Mirrors buildJiraBody:
// the bot marker is prepended so polling orchestrators can filter their
// own posts, and Title is folded into a bold-style line.  Bitbucket
// renders comments as Markdown so `*Title*` shows up as emphasis; the
// exact form mirrors the Jira variant for cross-surface consistency.
func buildBitbucketBody(msg Message, botMarker string) string {
	var b strings.Builder
	b.WriteString(botMarker)
	b.WriteByte(' ')
	if msg.Title != "" {
		b.WriteString("*")
		b.WriteString(msg.Title)
		b.WriteString("*\n\n")
	}
	b.WriteString(msg.Body)
	return b.String()
}
