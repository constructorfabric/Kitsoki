package starlark

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	goyaml "github.com/goccy/go-yaml"
)

// Record modes, mirroring Python VCR.py:
//
//   - none         replay only; a request that misses the cassette is an error.
//   - once         record when the cassette starts empty, otherwise replay-only
//     (a new request against an already-recorded cassette is an error).
//   - new_episodes replay matches and record (append) anything that misses.
//   - all          never replay; re-record every request, discarding prior
//     recordings.
const (
	RecordModeNone        = "none"
	RecordModeOnce        = "once"
	RecordModeNewEpisodes = "new_episodes"
	RecordModeAll         = "all"
)

// redactPlaceholder is what every redacted secret is replaced with on write.
const redactPlaceholder = "REDACTED"

// alwaysRedactHeaders are redacted on write regardless of filter_headers, so a
// first-run recording is safe to commit (matches the "no credentials in repo"
// rule). Compared case-insensitively.
var alwaysRedactHeaders = []string{"Authorization", "Cookie", "Set-Cookie", "Proxy-Authorization"}

// ValidateRecordMode reports whether mode is one this package understands. The
// empty string is treated as RecordModeNone.
func ValidateRecordMode(mode string) error {
	switch mode {
	case "", RecordModeNone, RecordModeOnce, RecordModeNewEpisodes, RecordModeAll:
		return nil
	default:
		return fmt.Errorf("starlark http cassette: record_mode %q is not supported (want none|once|new_episodes|all)", mode)
	}
}

// RecordReplayClient is an HTTPClient that replays from — and, per the cassette's
// record_mode, records to — an HTTPCassette, backed by a real transport for the
// recording path. It is the VCR.py-style engine behind the testrunner's
// starlark_http_cassette: seam.
//
// Construct it with NewRecordReplayClient; after the run, call Flush(path) to
// persist any newly recorded interactions (redacted) back to the cassette file.
type RecordReplayClient struct {
	mu        sync.Mutex
	cas       *HTTPCassette
	episodes  []*HTTPEpisode // pointers into cas.Exchanges (existing episodes)
	transport HTTPClient     // real network, used when recording; may be nil
	mode      string         // resolved: none|new_episodes|all (once resolved at construction)
	original  []HTTPEpisode  // snapshot of episodes as loaded (for write-back)
	recorded  []HTTPEpisode  // interactions captured this run
	exchanges []HTTPExchange // body-free summaries for the trace
	dirty     bool           // a new interaction was recorded
}

// NewRecordReplayClient builds a record/replay client over the cassette. mode is
// the effective record mode (see ValidateRecordMode); transport is the real
// HTTPClient used when recording (nil is allowed only for replay-only modes).
// The "once" mode is resolved here: if the cassette already has episodes it
// behaves as "none", otherwise as "new_episodes".
func NewRecordReplayClient(cas *HTTPCassette, mode string, transport HTTPClient) *RecordReplayClient {
	if cas == nil {
		cas = &HTTPCassette{Kind: "http_cassette"}
	}
	resolved := mode
	if resolved == "" {
		resolved = RecordModeNone
	}
	if resolved == RecordModeOnce {
		if len(cas.Exchanges) > 0 {
			resolved = RecordModeNone
		} else {
			resolved = RecordModeNewEpisodes
		}
	}
	c := &RecordReplayClient{
		cas:       cas,
		transport: transport,
		mode:      resolved,
		original:  append([]HTTPEpisode(nil), cas.Exchanges...),
	}
	// In "all" mode prior recordings are discarded; matching starts from empty.
	if resolved == RecordModeAll {
		c.episodes = nil
	} else {
		c.episodes = episodePtrs(cas)
	}
	return c
}

// Do replays a matching episode, or records the request via the real transport
// per the record mode. Ignored hosts always pass straight through.
func (c *RecordReplayClient) Do(ctx context.Context, method, url string, headers map[string]string, body []byte) (*HTTPResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cas.hostIgnored(url) {
		if c.transport == nil {
			return nil, fmt.Errorf("starlark http: %s %s is on ignore_hosts but no real transport is configured", method, url)
		}
		resp, err := c.transport.Do(ctx, method, url, headers, body)
		if err == nil && resp != nil {
			c.exchanges = append(c.exchanges, HTTPExchange{Method: method, URL: url, Status: resp.Status})
		}
		return resp, err
	}

	req := httpReq{method: method, url: url, headers: headers, body: body}

	// "all" never replays — every request is re-recorded.
	if c.mode != RecordModeAll {
		ep, err := matchEpisode(c.cas, c.episodes, req)
		if err != nil {
			return nil, err
		}
		if ep != nil {
			c.exchanges = append(c.exchanges, HTTPExchange{Method: method, URL: url, Status: ep.Response.Status})
			return cannedResponse(ep.Response), nil
		}
	}

	// Miss. Replay-only modes fail loudly.
	if c.mode == RecordModeNone {
		return nil, missError(method, url, c.episodes)
	}

	// Record: perform the real request and capture it.
	if c.transport == nil {
		return nil, fmt.Errorf("starlark http: record_mode %q needs a real transport but none was configured", c.mode)
	}
	resp, err := c.transport.Do(ctx, method, url, headers, body)
	if err != nil {
		return nil, err
	}
	c.recorded = append(c.recorded, HTTPEpisode{
		Request:  &HTTPRequestRecord{Method: method, URL: url, Headers: headers, Body: string(body)},
		Response: HTTPCanned{Status: resp.Status, Headers: resp.Headers, Body: string(resp.Body)},
	})
	c.dirty = true
	c.exchanges = append(c.exchanges, HTTPExchange{Method: method, URL: url, Status: resp.Status})
	return resp, nil
}

// Exchanges returns the body-free summaries recorded so far.
func (c *RecordReplayClient) Exchanges() []HTTPExchange {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]HTTPExchange, len(c.exchanges))
	copy(out, c.exchanges)
	return out
}

// Recorded reports whether any new interaction was recorded this run.
func (c *RecordReplayClient) Recorded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dirty
}

// Flush writes the cassette back to disk when recording produced new
// interactions (or in "all" mode, which always rewrites). Secrets are redacted
// in the written copy. It is a no-op for replay-only runs. serializer selects
// the on-disk format: "yaml" (default) or "json".
func (c *RecordReplayClient) Flush(path, serializer string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mode != RecordModeAll && !c.dirty {
		return nil
	}

	out := *c.cas
	var eps []HTTPEpisode
	if c.mode != RecordModeAll {
		eps = append(eps, c.original...)
	}
	eps = append(eps, c.recorded...)
	out.Exchanges = redactEpisodes(&out, eps)
	if out.Kind == "" {
		out.Kind = "http_cassette"
	}

	data, err := marshalCassette(&out, serializer)
	if err != nil {
		return err
	}
	return writeCassetteFile(path, data)
}

// hostIgnored reports whether the URL's host is on the cassette's ignore list
// (or localhost when ignore_localhost is set).
func (c *HTTPCassette) hostIgnored(rawURL string) bool {
	if len(c.IgnoreHosts) == 0 && !c.IgnoreLocalhost {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if c.IgnoreLocalhost && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return true
	}
	for _, h := range c.IgnoreHosts {
		if strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// ─── redaction (applied on write only) ──────────────────────────────────────

// redactEpisodes returns a redacted deep copy of eps per the cassette's filter_*
// settings. Only recorded (`request:`) episodes are redacted; legacy `match:`
// episodes are author-written and passed through untouched. Redaction applies
// only to the WRITTEN copy — the live run still serves real values to the script.
func redactEpisodes(cas *HTTPCassette, eps []HTTPEpisode) []HTTPEpisode {
	headerNames := append([]string{}, alwaysRedactHeaders...)
	headerNames = append(headerNames, cas.FilterHeaders...)

	out := make([]HTTPEpisode, len(eps))
	for i, ep := range eps {
		out[i] = ep
		if ep.Request != nil {
			req := *ep.Request
			req.Headers = redactHeaders(ep.Request.Headers, headerNames)
			req.URL = redactQuery(ep.Request.URL, cas.FilterQueryParameters)
			req.Body = redactPostData(ep.Request.Body, cas.FilterPostDataParameters)
			out[i].Request = &req
		}
		out[i].Response.Headers = redactHeaders(ep.Response.Headers, headerNames)
	}
	return out
}

// redactHeaders returns a copy of headers with any name in names (compared
// case-insensitively) replaced by the placeholder.
func redactHeaders(headers map[string]string, names []string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if containsFold(names, k) {
			out[k] = redactPlaceholder
		} else {
			out[k] = v
		}
	}
	return out
}

// redactQuery replaces the listed query parameters in a URL with the placeholder.
func redactQuery(rawURL string, params []string) string {
	if len(params) == 0 || rawURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	changed := false
	for _, p := range params {
		if _, ok := q[p]; ok {
			q.Set(p, redactPlaceholder)
			changed = true
		}
	}
	if !changed {
		return rawURL
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// redactPostData replaces the listed parameters in a request body. It handles
// application/x-www-form-urlencoded bodies and top-level JSON object keys; any
// other body is returned unchanged.
func redactPostData(body string, params []string) string {
	if len(params) == 0 || body == "" {
		return body
	}
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(body), &obj); err == nil {
			changed := false
			for _, p := range params {
				if _, ok := obj[p]; ok {
					obj[p] = redactPlaceholder
					changed = true
				}
			}
			if changed {
				if b, err := json.Marshal(obj); err == nil {
					return string(b)
				}
			}
			return body
		}
	}
	if vals, err := url.ParseQuery(body); err == nil {
		changed := false
		for _, p := range params {
			if _, ok := vals[p]; ok {
				vals.Set(p, redactPlaceholder)
				changed = true
			}
		}
		if changed {
			return vals.Encode()
		}
	}
	return body
}

// containsFold reports whether s is in list (case-insensitive).
func containsFold(list []string, s string) bool {
	for _, e := range list {
		if strings.EqualFold(e, s) {
			return true
		}
	}
	return false
}

// ─── serialization ──────────────────────────────────────────────────────────

// MarshalCassette serializes a cassette to bytes in the given format ("yaml" or
// "json"; empty == yaml). Map keys are emitted in sorted order for stable,
// reviewable diffs.
func MarshalCassette(cas *HTTPCassette, serializer string) ([]byte, error) {
	return marshalCassette(cas, serializer)
}

func marshalCassette(cas *HTTPCassette, serializer string) ([]byte, error) {
	switch strings.ToLower(serializer) {
	case "", "yaml", "yml":
		return goyaml.Marshal(cas)
	case "json":
		b, err := json.MarshalIndent(cas, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("starlark http cassette: marshal json: %w", err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("starlark http cassette: unknown serializer %q (want yaml|json)", serializer)
	}
}

// writeCassetteFile writes data to path, creating it fresh. A leading comment
// is not added here — the cassette is regenerated content, not hand-authored.
func writeCassetteFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("starlark http cassette: write %q: %w", path, err)
	}
	return nil
}
