package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// JiraConfig configures the Jira transport.
type JiraConfig struct {
	// BaseURL is the Jira root, e.g. "https://acme.atlassian.net".
	BaseURL string
	// Username for HTTP Basic auth.
	Username string
	// APIToken is the user's API token / PAT.
	APIToken string
	// BotMarker overrides DefaultBotMarker for this transport.
	BotMarker string
	// HTTPClient is used to make REST calls. nil → http.DefaultClient with a
	// 30s timeout.
	HTTPClient *http.Client
	// APIVersion is "2" (default) or "3"; v2 takes plain-text bodies and
	// works on both Cloud and self-hosted.
	APIVersion string
}

// JiraTransport posts comments to a Jira issue over the REST API, keyed by
// [SessionKey].Thread (the issue key). It authenticates with HTTP Basic using
// a username + API-token pair (Atlassian Cloud: the user's email plus a token
// from id.atlassian.com; self-hosted: a username plus a personal access
// token). It deliberately stays plain-text wiki (no ADF document tree) so a
// single code path works across Cloud and self-hosted — see the package
// Non-goals. The zero value is not usable; construct via [NewJiraTransport].
type JiraTransport struct {
	cfg    JiraConfig
	client *http.Client
}

// NewJiraTransport constructs a JiraTransport. Returns an error if the
// configuration is incomplete (BaseURL/Username/APIToken all required).
func NewJiraTransport(cfg JiraConfig) (*JiraTransport, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		return nil, fmt.Errorf("jira: BaseURL is required")
	}
	if strings.TrimSpace(cfg.Username) == "" {
		return nil, fmt.Errorf("jira: Username is required")
	}
	if strings.TrimSpace(cfg.APIToken) == "" {
		return nil, fmt.Errorf("jira: APIToken is required")
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "2"
	}
	if cfg.BotMarker == "" {
		cfg.BotMarker = DefaultBotMarker
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: httpClientTimeout}
	}
	return &JiraTransport{cfg: cfg, client: client}, nil
}

// ID reports the transport ID. Always "jira".
func (j *JiraTransport) ID() string { return "jira" }

// Post posts a comment to the issue named by key.Thread. Returns the
// Jira-assigned comment ID. Errors propagate (4xx/5xx → non-nil error).
func (j *JiraTransport) Post(ctx context.Context, key SessionKey, msg Message) (string, error) {
	if key.Thread == "" {
		return "", fmt.Errorf("jira: SessionKey.Thread (issue key) is required")
	}

	body := buildJiraBody(msg, j.cfg.BotMarker)

	url := fmt.Sprintf("%s/rest/api/%s/issue/%s/comment",
		j.cfg.BaseURL, j.cfg.APIVersion, key.Thread)

	payload := map[string]any{"body": body}
	enc, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("jira: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(enc))
	if err != nil {
		return "", fmt.Errorf("jira: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(j.cfg.Username, j.cfg.APIToken)

	resp, err := j.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("jira: do: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// Try to surface the API error message verbatim for diagnosis.
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("jira: POST %s: %s: %s", url, resp.Status, msg)
	}

	var parsed struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("jira: parse response: %w (body=%s)", err, string(respBody))
	}
	return parsed.ID, nil
}

// Close releases idle HTTP connections held by the transport client.
// http.Client doesn't require explicit cleanup but we honour the interface.
func (j *JiraTransport) Close() error {
	if t, ok := j.client.Transport.(*http.Transport); ok {
		t.CloseIdleConnections()
	}
	return nil
}

// buildJiraBody composes the comment text. Title is folded into a bold
// heading line (Jira wiki: `*text*`). The bot marker is prepended so
// orchestrators can filter their own posts on inbound polling.
//
// The body is run through sanitizeForJira so Markdown emitted by LLMs
// (the bugfix room's `summary_markdown` is plain Markdown) renders as
// styled Jira wiki rather than literal `**bold**` / `# heading` text.
// The title is intentionally NOT sanitised — it's already wrapped in
// `*…*` for Jira bold above and is short, plain text supplied by the
// orchestrator.
func buildJiraBody(msg Message, botMarker string) string {
	var b strings.Builder
	b.WriteString(botMarker)
	b.WriteByte(' ')
	if msg.Title != "" {
		b.WriteString("*")
		b.WriteString(msg.Title)
		b.WriteString("*\n\n")
	}
	b.WriteString(sanitizeForJira(msg.Body))
	return b.String()
}
