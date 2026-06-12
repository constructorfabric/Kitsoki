// Package harscrub provides deterministic anonymization of HAR 1.2 captures.
//
// Scrubbing is purely rule-based (no LLM, no network): sensitive headers,
// session-token query params, $HOME absolute paths, and caller-supplied secret
// patterns are redacted in place.
package harscrub

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// Redacted is the replacement value used for all scrubbed content.
const Redacted = "[REDACTED]"

// Minimal HAR 1.2 structures — only the fields harscrub needs are modeled.
// Unknown fields are preserved across Parse/Marshal via RawMessage passthrough
// where practical; here we model the subset we mutate.

// Har is the top-level HAR document.
type Har struct {
	Log Log `json:"log"`
}

// Log is the HAR log object.
type Log struct {
	Version string  `json:"version"`
	Creator Creator `json:"creator"`
	Entries []Entry `json:"entries"`
}

// Creator describes the tool that produced the HAR.
type Creator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Entry is a single request/response exchange.
type Entry struct {
	StartedDateTime string   `json:"startedDateTime"`
	Time            float64  `json:"time"`
	Request         Request  `json:"request"`
	Response        Response `json:"response"`
}

// Request models the request side of an entry.
type Request struct {
	Method      string      `json:"method"`
	URL         string      `json:"url"`
	Headers     []NameValue `json:"headers"`
	QueryString []NameValue `json:"queryString"`
	PostData    *PostData   `json:"postData,omitempty"`
}

// Response models the response side of an entry.
type Response struct {
	Status  int         `json:"status"`
	Headers []NameValue `json:"headers"`
	Content *Content    `json:"content,omitempty"`
}

// PostData holds a request body.
type PostData struct {
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// Content holds a response body.
type Content struct {
	Size     int    `json:"size,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
}

// NameValue is a HAR name/value pair (header, query param, etc.).
type NameValue struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ScrubOptions configures Scrub.
type ScrubOptions struct {
	// Home is an absolute path prefix replaced with "$HOME" everywhere it
	// appears. Empty disables home-path redaction.
	Home string
	// SecretPatterns are redacted wherever they match (substring → [REDACTED]).
	SecretPatterns []*regexp.Regexp
}

// defaultSecretPatterns matches common credential shapes that leak in free text
// (response bodies, console output, error messages, serialized rrweb DOM) where
// the name-based header/query rules don't reach. Conservative by design: each
// pattern targets a high-signal token shape so it won't shred ordinary prose.
var defaultSecretPatterns = []*regexp.Regexp{
	// Authorization-style bearer tokens embedded in text, e.g. `Bearer eyJ...`.
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-]+`),
	// OpenAI / Anthropic style API keys: sk-..., sk-ant-..., etc.
	regexp.MustCompile(`\bsk-[A-Za-z0-9-]{16,}`),
	// GitHub tokens (ghp_, gho_, ghs_, ghr_, github_pat_).
	regexp.MustCompile(`\b(?:gh[posru]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`),
	// AWS access key IDs.
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	// Google API keys.
	regexp.MustCompile(`\bAIza[0-9A-Za-z._\-]{20,}`),
	// Slack tokens (xoxb-, xoxp-, xoxa-, xoxr-).
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`),
	// JSON web tokens (three base64url segments).
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+`),
	// Private key PEM headers (catch the block opener; the body follows).
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	// Generic `secret`/`token`/`api[_-]?key`/`password` = value assignments in
	// text or JSON, e.g. `"api_key": "abcd"` or `password=hunter2`.
	regexp.MustCompile(`(?i)\b(?:secret|token|api[_-]?key|password|passwd|access[_-]?key)\b\s*["']?\s*[:=]\s*["']?[A-Za-z0-9._\-/+]{6,}`),
}

// DefaultSecretPatterns returns a fresh copy of the built-in secret-pattern set
// used to redact credential-shaped substrings from free text (HAR bodies,
// console logs, error payloads, serialized rrweb events). Callers pass it via
// ScrubOptions.SecretPatterns; it is the production default so ScrubString is
// more than $HOME substitution.
func DefaultSecretPatterns() []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(defaultSecretPatterns))
	copy(out, defaultSecretPatterns)
	return out
}

// redactHeaderNames is the set of header names (lowercased) that are redacted.
var redactHeaderNames = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"set-cookie":          true,
	"proxy-authorization": true,
}

// redactQueryParams is the set of query param names (lowercased) that are redacted.
var redactQueryParams = map[string]bool{
	"token":        true,
	"access_token": true,
	"session":      true,
	"sid":          true,
	"api_key":      true,
}

// Scrub anonymizes the HAR document in place per the configured options.
func Scrub(h *Har, opts ScrubOptions) {
	if h == nil {
		return
	}
	for i := range h.Log.Entries {
		scrubEntry(&h.Log.Entries[i], opts)
	}
}

func scrubEntry(e *Entry, opts ScrubOptions) {
	scrubHeaders(e.Request.Headers)
	scrubHeaders(e.Response.Headers)

	scrubQueryParams(e.Request.QueryString)
	e.Request.URL = scrubURL(e.Request.URL)

	// Apply free-text redactions (home + secret patterns) across all strings.
	apply := func(s string) string { return ScrubString(s, opts) }

	e.Request.URL = apply(e.Request.URL)
	for j := range e.Request.Headers {
		e.Request.Headers[j].Value = apply(e.Request.Headers[j].Value)
	}
	for j := range e.Request.QueryString {
		e.Request.QueryString[j].Value = apply(e.Request.QueryString[j].Value)
	}
	for j := range e.Response.Headers {
		e.Response.Headers[j].Value = apply(e.Response.Headers[j].Value)
	}
	if e.Request.PostData != nil {
		e.Request.PostData.Text = apply(e.Request.PostData.Text)
	}
	if e.Response.Content != nil {
		e.Response.Content.Text = apply(e.Response.Content.Text)
	}
}

func scrubHeaders(headers []NameValue) {
	for i := range headers {
		if redactHeaderNames[strings.ToLower(headers[i].Name)] {
			headers[i].Value = Redacted
		}
	}
}

func scrubQueryParams(params []NameValue) {
	for i := range params {
		if redactQueryParams[strings.ToLower(params[i].Name)] {
			params[i].Value = Redacted
		}
	}
}

// scrubURL redacts known session-token query params inside a URL string.
func scrubURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	changed := false
	for key := range q {
		if redactQueryParams[strings.ToLower(key)] {
			q.Set(key, Redacted)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	// url.Values.Encode sorts keys; rebuild RawQuery to keep it deterministic.
	u.RawQuery = q.Encode()
	return u.String()
}

// ScrubString applies the same free-text redactions Scrub applies to HAR
// string fields (home-path substitution + secret-pattern redaction) to an
// arbitrary string. Callers use it to scrub client-supplied prose such as
// console logs, rrweb events, and error payloads before they are written
// alongside a bug report. It is the exported form of the per-field redactor.
func ScrubString(s string, opts ScrubOptions) string {
	if s == "" {
		return s
	}
	if opts.Home != "" {
		s = strings.ReplaceAll(s, opts.Home, "$HOME")
	}
	for _, re := range opts.SecretPatterns {
		if re == nil {
			continue
		}
		s = re.ReplaceAllString(s, Redacted)
	}
	return s
}

// ParseHar decodes HAR JSON bytes into a Har.
func ParseHar(data []byte) (*Har, error) {
	var h Har
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// Marshal encodes a Har as indented JSON.
func Marshal(h *Har) ([]byte, error) {
	return json.MarshalIndent(h, "", "  ")
}
